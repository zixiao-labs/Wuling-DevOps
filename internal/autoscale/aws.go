package autoscale

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// awsProvider launches/terminates EC2 instances via the EC2 query API, signed
// with SigV4. We call the REST API directly (rather than vendoring the AWS SDK)
// to keep the wuling-api binary dependency-light; the signing below is the
// standard AWS Signature Version 4.
type awsProvider struct {
	pool  AWSPool
	creds awsCreds
	http  *http.Client
}

func newAWSProvider(pool Pool, creds awsCreds) (Provider, error) {
	if pool.AWS == nil {
		return nil, fmt.Errorf("aws pool config missing")
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return nil, fmt.Errorf("aws credentials missing access_key_id/secret_access_key")
	}
	return &awsProvider{pool: *pool.AWS, creds: creds, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (p *awsProvider) Name() string { return "aws" }

func (p *awsProvider) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
	params := url.Values{}
	params.Set("Action", "RunInstances")
	params.Set("Version", "2016-11-15")
	params.Set("ImageId", p.pool.AMI)
	params.Set("InstanceType", p.pool.InstanceType)
	params.Set("MinCount", "1")
	params.Set("MaxCount", "1")
	params.Set("UserData", base64.StdEncoding.EncodeToString([]byte(spec.UserData)))
	if p.pool.SubnetID != "" {
		params.Set("SubnetId", p.pool.SubnetID)
	}
	for i, sg := range p.pool.SecurityGroupIDs {
		params.Set(fmt.Sprintf("SecurityGroupId.%d", i+1), sg)
	}
	if p.pool.IAMInstanceProfile != "" {
		params.Set("IamInstanceProfile.Name", p.pool.IAMInstanceProfile)
	}
	if p.pool.Spot {
		params.Set("InstanceMarketOptions.MarketType", "spot")
	}
	// Tag the instance so it's identifiable in the console.
	params.Set("TagSpecification.1.ResourceType", "instance")
	params.Set("TagSpecification.1.Tag.1.Key", "Name")
	params.Set("TagSpecification.1.Tag.1.Value", spec.RunnerName)
	params.Set("TagSpecification.1.Tag.2.Key", "managed-by")
	params.Set("TagSpecification.1.Tag.2.Value", "wuling-autoscaler")

	body, err := p.call(ctx, params)
	if err != nil {
		return Instance{}, err
	}
	var resp struct {
		Instances []struct {
			InstanceID string `xml:"instanceId"`
		} `xml:"instancesSet>item"`
	}
	if err := xml.Unmarshal(body, &resp); err != nil {
		return Instance{}, fmt.Errorf("parse RunInstances response: %w", err)
	}
	if len(resp.Instances) == 0 || resp.Instances[0].InstanceID == "" {
		return Instance{}, fmt.Errorf("RunInstances returned no instance id")
	}
	return Instance{ExternalID: resp.Instances[0].InstanceID}, nil
}

func (p *awsProvider) Terminate(ctx context.Context, externalID string) error {
	params := url.Values{}
	params.Set("Action", "TerminateInstances")
	params.Set("Version", "2016-11-15")
	params.Set("InstanceId.1", externalID)
	_, err := p.call(ctx, params)
	return err
}

// call signs and POSTs an EC2 query-API request, returning the response body
// or an error carrying the AWS error text.
func (p *awsProvider) call(ctx context.Context, params url.Values) ([]byte, error) {
	const service = "ec2"
	host := fmt.Sprintf("ec2.%s.amazonaws.com", p.pool.Region)
	endpoint := "https://" + host + "/"
	payload := params.Encode()

	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := sha256Hex([]byte(payload))
	canonicalHeaders := fmt.Sprintf("content-type:application/x-www-form-urlencoded\nhost:%s\nx-amz-date:%s\n", host, amzDate)
	signedHeaders := "content-type;host;x-amz-date"
	canonicalRequest := strings.Join([]string{
		"POST", "/", "", canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, p.pool.Region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+p.creds.SecretAccessKey), dateStamp)
	kRegion := hmacSHA256(kDate, p.pool.Region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	authz := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.creds.AccessKeyID, scope, signedHeaders, signature)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Authorization", authz)
	if p.creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", p.creds.SessionToken)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aws ec2 %s: %s", resp.Status, awsErrorText(respBody))
	}
	return respBody, nil
}

func awsErrorText(body []byte) string {
	var e struct {
		Errors []struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Errors>Error"`
	}
	if err := xml.Unmarshal(body, &e); err == nil && len(e.Errors) > 0 {
		return e.Errors[0].Code + ": " + e.Errors[0].Message
	}
	return strings.TrimSpace(string(body))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
