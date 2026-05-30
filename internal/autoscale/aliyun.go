package autoscale

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// aliyunProvider launches/terminates ECS instances via the Aliyun ECS RPC API,
// signed per the classic HMAC-SHA1 RPC scheme. Direct REST (no vendored SDK)
// keeps the binary light.
type aliyunProvider struct {
	pool  AliyunPool
	creds aliyunCreds
	http  *http.Client
}

func newAliyunProvider(pool Pool, creds aliyunCreds) (Provider, error) {
	if pool.Aliyun == nil {
		return nil, fmt.Errorf("aliyun pool config missing")
	}
	if creds.AccessKeyID == "" || creds.AccessKeySecret == "" {
		return nil, fmt.Errorf("aliyun credentials missing access_key_id/access_key_secret")
	}
	return &aliyunProvider{pool: *pool.Aliyun, creds: creds, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (p *aliyunProvider) Name() string { return "aliyun" }

func (p *aliyunProvider) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
	params := map[string]string{
		"Action":          "RunInstances",
		"RegionId":        p.pool.Region,
		"ImageId":         p.pool.ImageID,
		"InstanceType":    p.pool.InstanceType,
		"SecurityGroupId": p.pool.SecurityGroupID,
		"VSwitchId":       p.pool.VSwitchID,
		"Amount":          "1",
		"InstanceName":    spec.RunnerName,
		"UserData":        base64.StdEncoding.EncodeToString([]byte(spec.UserData)),
	}
	if p.pool.ZoneID != "" {
		params["ZoneId"] = p.pool.ZoneID
	}
	if p.pool.InternetMaxBandwidthOut > 0 {
		params["InternetMaxBandwidthOut"] = strconv.Itoa(p.pool.InternetMaxBandwidthOut)
	}
	if p.pool.Spot {
		params["SpotStrategy"] = "SpotAsPriceGo"
	}

	body, err := p.call(ctx, params)
	if err != nil {
		return Instance{}, err
	}
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

func (p *aliyunProvider) Terminate(ctx context.Context, externalID string) error {
	_, err := p.call(ctx, map[string]string{
		"Action":     "DeleteInstance",
		"RegionId":   p.pool.Region,
		"InstanceId": externalID,
		"Force":      "true",
	})
	return err
}

// call signs and sends an Aliyun ECS RPC request (HMAC-SHA1, signature v1.0).
func (p *aliyunProvider) call(ctx context.Context, biz map[string]string) ([]byte, error) {
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

	// Canonicalized query string: sorted, percent-encoded per Aliyun rules.
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
	stringToSign := "GET&" + aliyunEncode("/") + "&" + aliyunEncode(canonical)

	mac := hmac.New(sha1.New, []byte(p.creds.AccessKeySecret+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	endpoint := fmt.Sprintf("https://ecs.%s.aliyuncs.com/?%s&Signature=%s",
		p.pool.Region, canonical, aliyunEncode(signature))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aliyun ecs %s: %s", resp.Status, aliyunErrorText(respBody))
	}
	return respBody, nil
}

func aliyunErrorText(body []byte) string {
	var e struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Code != "" {
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
