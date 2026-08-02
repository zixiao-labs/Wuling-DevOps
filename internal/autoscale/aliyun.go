package autoscale

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// aliyunProvider launches/terminates ECS instances via the Aliyun ECS RPC API,
// signed per the classic HMAC-SHA1 RPC scheme. Direct REST (no vendored SDK)
// keeps the binary light.
type aliyunProvider struct {
	pool     AliyunPool
	creds    aliyunCreds
	password string
	http     *http.Client
}

func newAliyunProvider(pool Pool, creds aliyunCreds, password string) (Provider, error) {
	if pool.Aliyun == nil {
		return nil, fmt.Errorf("aliyun pool config missing")
	}
	if creds.AccessKeyID == "" || creds.AccessKeySecret == "" {
		return nil, fmt.Errorf("aliyun credentials missing access_key_id/access_key_secret")
	}
	return &aliyunProvider{
		pool:     *pool.Aliyun,
		creds:    creds,
		password: password,
		http:     &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (p *aliyunProvider) Name() string { return ProviderAliyun }

func (p *aliyunProvider) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
	if len(spec.UserData) > 32*1024 {
		return Instance{}, fmt.Errorf("user-data exceeds 32 KiB before base64 encoding (InvalidUserData.SizeExceeded)")
	}

	types, err := p.resolveInstanceTypes()
	if err != nil {
		return Instance{}, err
	}

	now := time.Now()
	var lastErr error
	for i, instanceType := range types {
		params := p.runInstancesParams(spec, instanceType, now)
		body, err := p.call(ctx, params, callOpts{MaxAttempts: 3, Method: http.MethodPost})
		if err == nil {
			var resp struct {
				InstanceIDSets struct {
					InstanceIDSet []string `json:"InstanceIdSet"`
				} `json:"InstanceIdSets"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				return Instance{}, fmt.Errorf("parse RunInstances response: %w", err)
			}
			if len(resp.InstanceIDSets.InstanceIDSet) == 0 {
				return Instance{}, fmt.Errorf("RunInstances returned no instance id")
			}
			return Instance{ExternalID: resp.InstanceIDSets.InstanceIDSet[0]}, nil
		}
		lastErr = err
		var apiErr *aliyunAPIError
		if errors.As(err, &apiErr) && apiErr.outOfCapacity() && i+1 < len(types) {
			continue // try next instance type
		}
		return Instance{}, err
	}
	return Instance{}, lastErr
}

func (p *aliyunProvider) resolveInstanceTypes() ([]string, error) {
	if p.pool.InstanceType != "" {
		return []string{p.pool.InstanceType}, nil
	}
	if len(p.pool.InstanceTypes) > 0 {
		return p.pool.InstanceTypes, nil
	}
	return nil, fmt.Errorf("instance_type or instance_types is required")
}

// runInstancesParams renders the RunInstances business parameters for one
// launch. Every value must be computed once before the retry loop: ClientToken
// makes the call idempotent only while the other parameters are byte-identical.
func (p *aliyunProvider) runInstancesParams(spec LaunchSpec, instanceType string, now time.Time) map[string]string {
	params := map[string]string{
		"Action":          "RunInstances",
		"RegionId":        p.pool.Region,
		"ImageId":         p.pool.ImageID,
		"InstanceType":    instanceType,
		"SecurityGroupId": p.pool.SecurityGroupID,
		"VSwitchId":       p.pool.VSwitchID,
		"Amount":          "1",
		"InstanceName":    spec.RunnerName,
		"HostName":        aliyunHostName(spec.RunnerName, spec.Pool.OS),
		"UserData":        base64.StdEncoding.EncodeToString([]byte(spec.UserData)),
		"IoOptimized":     "optimized",
		"InstanceChargeType": orDefault(p.pool.InstanceChargeType, "PostPaid"),
		"ClientToken":     spec.IdempotencyKey(),
	}
	if p.pool.ZoneID != "" {
		params["ZoneId"] = p.pool.ZoneID
	}
	if p.pool.ResourceGroupID != "" {
		params["ResourceGroupId"] = p.pool.ResourceGroupID
	}
	if p.pool.RAMRoleName != "" {
		params["RamRoleName"] = p.pool.RAMRoleName
	}
	if p.pool.InternetMaxBandwidthOut > 0 {
		params["InternetMaxBandwidthOut"] = strconv.Itoa(p.pool.InternetMaxBandwidthOut)
		if p.pool.InternetChargeType != "" {
			params["InternetChargeType"] = p.pool.InternetChargeType
		}
	}

	sizeSpec := p.pool.SystemDiskSize
	if sizeSpec == "" {
		sizeSpec = spec.TierSpec.Storage
	}
	if gib, ok := ParseSizeGiB(sizeSpec); ok {
		params["SystemDisk.Size"] = strconv.Itoa(gib)
	}
	if p.pool.SystemDiskCategory != "" {
		params["SystemDisk.Category"] = p.pool.SystemDiskCategory
	}
	if p.pool.SystemDiskPerformanceLevel != "" {
		params["SystemDisk.PerformanceLevel"] = p.pool.SystemDiskPerformanceLevel
	}
	if gib, ok := ParseSizeGiB(p.pool.DataDiskSize); ok {
		params["DataDisk.1.Size"] = strconv.Itoa(gib)
		params["DataDisk.1.DeleteWithInstance"] = "true"
		if p.pool.DataDiskCategory != "" {
			params["DataDisk.1.Category"] = p.pool.DataDiskCategory
		}
	}

	switch {
	case p.pool.PasswordInherit:
		params["PasswordInherit"] = "true"
	case p.password != "":
		params["Password"] = p.password
	}
	if p.pool.KeyPairName != "" && spec.Pool.OS != model.OSWindows {
		params["KeyPairName"] = p.pool.KeyPairName
	}

	if p.pool.Spot {
		strategy := orDefault(p.pool.SpotStrategy, "SpotAsPriceGo")
		params["SpotStrategy"] = strategy
		if strategy == "SpotWithPriceLimit" {
			params["SpotPriceLimit"] = strconv.FormatFloat(p.pool.SpotPriceLimit, 'f', 3, 64)
		}
		if p.pool.SpotDuration != nil {
			params["SpotDuration"] = strconv.Itoa(*p.pool.SpotDuration)
		}
		if p.pool.SpotInterruptionBehavior != "" {
			params["SpotInterruptionBehavior"] = p.pool.SpotInterruptionBehavior
		}
	}
	if h := p.pool.AutoReleaseHours; h > 0 && params["InstanceChargeType"] == "PostPaid" {
		params["AutoReleaseTime"] = now.UTC().Add(time.Duration(h) * time.Hour).Format("2006-01-02T15:04:05Z")
	}

	tags := [][2]string{
		{"managed-by", "wuling-autoscaler"},
		{"wuling-org", spec.OrgID.String()},
		{"wuling-pool", spec.Pool.Name},
		{"wuling-runner", spec.RunnerName},
	}
	for _, k := range sortedKeys(p.pool.Tags) {
		tags = append(tags, [2]string{k, p.pool.Tags[k]})
	}
	for i, t := range tags {
		if i >= 20 {
			break
		}
		params[fmt.Sprintf("Tag.%d.Key", i+1)] = t[0]
		params[fmt.Sprintf("Tag.%d.Value", i+1)] = t[1]
	}
	return params
}

// aliyunHostName derives a legal ECS hostname from the runner name. Windows
// hostnames are capped at 15 chars, may not contain dots, and may not be all
// digits; Windows gets a short deterministic form built from the name's hex suffix.
func aliyunHostName(runnerName, os string) string {
	if os != model.OSWindows {
		return runnerName
	}
	if idx := strings.LastIndex(runnerName, "-"); idx >= 0 {
		suffix := runnerName[idx+1:]
		if len(suffix) == 8 && isHexString(suffix) {
			return "w" + suffix
		}
	}
	// Fallback: strip leading/trailing hyphens, truncate to 15, force a letter at index 0.
	name := strings.Trim(runnerName, "-")
	if len(name) > 15 {
		name = name[:15]
	}
	name = strings.Trim(name, "-")
	if name == "" {
		return "wrunner"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "w" + name
		if len(name) > 15 {
			name = name[:15]
		}
	}
	if isAllDigits(name) {
		return "w" + name
	}
	return name
}

func isHexString(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (p *aliyunProvider) Terminate(ctx context.Context, externalID string) error {
	_, err := p.call(ctx, map[string]string{
		"Action":     "DeleteInstance",
		"RegionId":   p.pool.Region,
		"InstanceId": externalID,
		"Force":      "true",
	}, callOpts{MaxAttempts: 3})
	if err != nil {
		var apiErr *aliyunAPIError
		if errors.As(err, &apiErr) && apiErr.notFound() {
			return nil
		}
	}
	return err
}

type callOpts struct {
	MaxAttempts int
	Method      string
}

// aliyunAPIError is the ECS RPC error envelope, kept typed so callers branch on
// the documented Code instead of string-matching a message.
type aliyunAPIError struct {
	HTTPStatus int
	Code       string
	Message    string
	RequestID  string
}

func (e *aliyunAPIError) Error() string {
	return fmt.Sprintf("aliyun ecs %d %s: %s (RequestId=%s)", e.HTTPStatus, e.Code, e.Message, e.RequestID)
}

func (e *aliyunAPIError) retryable() bool {
	switch {
	case e.HTTPStatus == http.StatusTooManyRequests:
		return true
	case e.HTTPStatus >= 500:
		return true
	case strings.HasPrefix(e.Code, "Throttling"):
		return true
	}
	return false
}

func (e *aliyunAPIError) outOfCapacity() bool {
	switch e.Code {
	case "OperationDenied.NoStock", "InvalidOperation.PublicIpAddressNoStock",
		"Invalid.PrivatePoolOptions.NoStock", "LackResource", "Account.Arrearage":
		return true
	}
	return strings.HasPrefix(e.Code, "QuotaExceed")
}

func (e *aliyunAPIError) notFound() bool {
	return strings.HasPrefix(e.Code, "InvalidInstanceId.NotFound")
}

// IsAliyunOutOfCapacity reports whether err is an ECS stock/quota refusal.
func IsAliyunOutOfCapacity(err error) bool {
	var apiErr *aliyunAPIError
	return errors.As(err, &apiErr) && apiErr.outOfCapacity()
}

func (p *aliyunProvider) call(ctx context.Context, biz map[string]string, opts callOpts) ([]byte, error) {
	attempts := opts.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	method := opts.Method
	if method == "" {
		method = http.MethodGet
	}

	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffDelay(attempt - 1)):
			}
		}
		body, err := p.doCall(ctx, biz, method)
		if err == nil {
			return body, nil
		}
		last = err
		var apiErr *aliyunAPIError
		if errors.As(err, &apiErr) && !apiErr.retryable() {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, last
}

func backoffDelay(n int) time.Duration {
	const base, ceiling = 500 * time.Millisecond, 8 * time.Second
	d := min(base<<n, ceiling)
	return time.Duration(rand.Int64N(int64(d)))
}

func (p *aliyunProvider) doCall(ctx context.Context, biz map[string]string, method string) ([]byte, error) {
	params := map[string]string{
		"Format":           "JSON",
		"Version":          "2014-05-26",
		"AccessKeyId":      p.creds.AccessKeyID,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   uuid.NewString(),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	for k, v := range biz {
		params[k] = v
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, aliyunEncode(k)+"="+aliyunEncode(params[k]))
	}
	canonical := strings.Join(pairs, "&")
	stringToSign := method + "&" + aliyunEncode("/") + "&" + aliyunEncode(canonical)

	mac := hmac.New(sha1.New, []byte(p.creds.AccessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	endpoint := fmt.Sprintf("https://ecs.%s.aliyuncs.com/", p.pool.Region)
	var req *http.Request
	var err error
	if method == http.MethodPost {
		form := canonical + "&Signature=" + aliyunEncode(signature)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		u := endpoint + "?" + canonical + "&Signature=" + aliyunEncode(signature)
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read aliyun ecs response: %w", err)
	}
	if resp.StatusCode >= 300 {
		apiErr := parseAliyunAPIError(resp.StatusCode, respBody)
		return nil, apiErr
	}
	return respBody, nil
}

func parseAliyunAPIError(status int, body []byte) *aliyunAPIError {
	var e struct {
		Code      string `json:"Code"`
		Message   string `json:"Message"`
		RequestID string `json:"RequestId"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Code != "" {
		return &aliyunAPIError{
			HTTPStatus: status,
			Code:       e.Code,
			Message:    e.Message,
			RequestID:  e.RequestID,
		}
	}
	return &aliyunAPIError{
		HTTPStatus: status,
		Code:       "Unknown",
		Message:    strings.TrimSpace(string(body)),
	}
}

func aliyunErrorText(body []byte) string {
	if e := parseAliyunAPIError(0, body); e.Code != "" {
		return e.Code + ": " + e.Message
	}
	return strings.TrimSpace(string(body))
}

// aliyunEncode is RFC3986 percent-encoding with Aliyun's specific tweaks
// (+ -> %20, * -> %2A, %7E -> ~).
func aliyunEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}
