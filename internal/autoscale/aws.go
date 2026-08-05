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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	if err := p.verifyTopology(ctx); err != nil {
		return Instance{}, err
	}
	params := p.runInstancesParams(spec)

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

// verifyTopology makes the explicit v2 VPC contract real before a billable
// RunInstances request. SubnetId alone selects placement in EC2, so without
// these Describe calls a typo in vpc_id would silently be accepted and make
// GitOps/audit records claim the wrong network boundary.
func (p *awsProvider) verifyTopology(ctx context.Context) error {
	if p.pool.VPCID == "" || p.pool.SubnetID == "" || len(p.pool.SecurityGroupIDs) == 0 {
		// v1 allowed EC2's implicit/default-network behavior. v2 validation
		// requires all three fields, so every v2 launch takes this path.
		return nil
	}
	subnets := url.Values{}
	subnets.Set("Action", "DescribeSubnets")
	subnets.Set("Version", "2016-11-15")
	subnets.Set("SubnetId.1", p.pool.SubnetID)
	body, err := p.call(ctx, subnets)
	if err != nil {
		return fmt.Errorf("verify aws subnet/VPC: %w", err)
	}
	if err := validateAWSSubnetVPC(body, p.pool.SubnetID, p.pool.VPCID); err != nil {
		return err
	}

	groups := url.Values{}
	groups.Set("Action", "DescribeSecurityGroups")
	groups.Set("Version", "2016-11-15")
	for i, id := range p.pool.SecurityGroupIDs {
		groups.Set(fmt.Sprintf("GroupId.%d", i+1), id)
	}
	body, err = p.call(ctx, groups)
	if err != nil {
		return fmt.Errorf("verify aws security group/VPC: %w", err)
	}
	return validateAWSSecurityGroupVPC(body, p.pool.SecurityGroupIDs, p.pool.VPCID)
}

func validateAWSSubnetVPC(body []byte, subnetID, vpcID string) error {
	var subnetResp struct {
		Subnets []struct {
			ID    string `xml:"subnetId"`
			VPCID string `xml:"vpcId"`
		} `xml:"subnetSet>item"`
	}
	if err := xml.Unmarshal(body, &subnetResp); err != nil {
		return fmt.Errorf("parse DescribeSubnets response: %w", err)
	}
	if len(subnetResp.Subnets) != 1 || subnetResp.Subnets[0].ID != subnetID || subnetResp.Subnets[0].VPCID == "" {
		return fmt.Errorf("DescribeSubnets did not return configured subnet %q", subnetID)
	}
	if subnetResp.Subnets[0].VPCID != vpcID {
		return fmt.Errorf("configured aws subnet %q belongs to VPC %q, not vpc_id %q",
			subnetID, subnetResp.Subnets[0].VPCID, vpcID)
	}
	return nil
}

func validateAWSSecurityGroupVPC(body []byte, groupIDs []string, vpcID string) error {
	var groupResp struct {
		Groups []struct {
			ID    string `xml:"groupId"`
			VPCID string `xml:"vpcId"`
		} `xml:"securityGroupInfo>item"`
	}
	if err := xml.Unmarshal(body, &groupResp); err != nil {
		return fmt.Errorf("parse DescribeSecurityGroups response: %w", err)
	}
	if len(groupResp.Groups) != len(groupIDs) {
		return fmt.Errorf("DescribeSecurityGroups returned %d groups, want %d", len(groupResp.Groups), len(groupIDs))
	}
	expected := make(map[string]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		expected[id] = struct{}{}
	}
	for _, group := range groupResp.Groups {
		if _, ok := expected[group.ID]; !ok {
			return fmt.Errorf("DescribeSecurityGroups returned unexpected security group %q", group.ID)
		}
		if group.VPCID == "" || group.VPCID != vpcID {
			return fmt.Errorf("configured aws security group %q does not belong to vpc_id %q", group.ID, vpcID)
		}
		delete(expected, group.ID)
	}
	if len(expected) != 0 {
		return fmt.Errorf("DescribeSecurityGroups did not return every configured security group")
	}
	return nil
}

// runInstancesParams renders the EC2 Query API request without adding a root
// block-device mapping. Leaving the image's root mapping untouched preserves
// the AMI's boot-disk behavior; every mapping rendered here is a data disk.
func (p *awsProvider) runInstancesParams(spec LaunchSpec) url.Values {
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
	for i, disk := range p.pool.DataDisks {
		index := i + 1
		prefix := fmt.Sprintf("BlockDeviceMapping.%d.", index)
		params.Set(prefix+"DeviceName", awsDataDiskDeviceName(disk, i))
		if gib, ok := ParseSizeGiB(disk.Size); ok {
			params.Set(prefix+"Ebs.VolumeSize", strconv.Itoa(gib))
		}
		if disk.Category != "" {
			params.Set(prefix+"Ebs.VolumeType", disk.Category)
		}
		params.Set(prefix+"Ebs.DeleteOnTermination", strconv.FormatBool(disk.DeleteWithInstance))
		params.Set(prefix+"Ebs.Encrypted", strconv.FormatBool(disk.Encrypted))
	}
	// Tag the instance so it is traceable in the provider console. The
	// isolated-job id is a non-secret audit join key for per-job VM cleanup.
	tags := [][2]string{
		{"Name", spec.RunnerName},
		{"managed-by", "wuling-autoscaler"},
		{"wuling-org", spec.OrgID.String()},
		{"wuling-pool", spec.Pool.Name},
		{"wuling-runner", spec.RunnerName},
		{"wuling-runner-id", spec.RunnerID.String()},
	}
	if spec.IsolatedJobID != uuid.Nil {
		tags = append(tags, [2]string{"wuling-isolated-job", spec.IsolatedJobID.String()})
	}
	params.Set("TagSpecification.1.ResourceType", "instance")
	for i, tag := range tags {
		params.Set(fmt.Sprintf("TagSpecification.1.Tag.%d.Key", i+1), tag[0])
		params.Set(fmt.Sprintf("TagSpecification.1.Tag.%d.Value", i+1), tag[1])
	}
	return params
}

// awsDataDiskDeviceName supplies a deterministic non-root mapping when an
// operator does not set device_name. AWS device names are advisory on Nitro
// instances, but still identify the EBS mapping in the RunInstances request.
func awsDataDiskDeviceName(disk DataDisk, index int) string {
	if disk.DeviceName != "" {
		return disk.DeviceName
	}
	return fmt.Sprintf("/dev/sd%c", 'f'+rune(index))
}

func (p *awsProvider) Terminate(ctx context.Context, externalID string) error {
	params := url.Values{}
	params.Set("Action", "TerminateInstances")
	params.Set("Version", "2016-11-15")
	params.Set("InstanceId.1", externalID)
	_, err := p.call(ctx, params)
	return err
}

// FindRunnerInstance recovers an instance id after a post-launch database
// write failed. runner-id is immutable and unique to a generated runner row,
// unlike a human-readable name that operators may reuse.
func (p *awsProvider) FindRunnerInstance(ctx context.Context, runnerID uuid.UUID) (Instance, bool, error) {
	params := url.Values{}
	params.Set("Action", "DescribeInstances")
	params.Set("Version", "2016-11-15")
	params.Set("Filter.1.Name", "tag:wuling-runner-id")
	params.Set("Filter.1.Value.1", runnerID.String())
	body, err := p.call(ctx, params)
	if err != nil {
		return Instance{}, false, fmt.Errorf("find aws runner instance: %w", err)
	}
	instanceID, found, err := parseAWSRunnerInstance(body)
	if err != nil {
		return Instance{}, false, err
	}
	if !found {
		return Instance{}, false, nil
	}
	return Instance{ExternalID: instanceID}, true, nil
}

func parseAWSRunnerInstance(body []byte) (string, bool, error) {
	var response struct {
		Reservations []struct {
			Instances []struct {
				ID string `xml:"instanceId"`
			} `xml:"instancesSet>item"`
		} `xml:"reservationSet>item"`
	}
	if err := xml.Unmarshal(body, &response); err != nil {
		return "", false, fmt.Errorf("parse DescribeInstances response: %w", err)
	}
	var instanceID string
	for _, reservation := range response.Reservations {
		for _, instance := range reservation.Instances {
			if instance.ID == "" {
				continue
			}
			if instanceID != "" && instanceID != instance.ID {
				return "", false, fmt.Errorf("DescribeInstances returned multiple instances for one runner id")
			}
			instanceID = instance.ID
		}
	}
	return instanceID, instanceID != "", nil
}

// call signs and POSTs an EC2 query-API request, returning the response body
// or an error carrying the AWS error text.
func (p *awsProvider) call(ctx context.Context, params url.Values) (respBody []byte, err error) {
	const service = "ec2"
	host := fmt.Sprintf("ec2.%s.amazonaws.com", p.pool.Region)
	endpoint := "https://" + host + "/"
	payload := params.Encode()

	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := sha256Hex([]byte(payload))
	// Canonical headers must be sorted by lowercased name. With temporary
	// credentials we also send X-Amz-Security-Token, which must therefore be
	// part of the signed set or AWS rejects the signature (403). It sorts after
	// x-amz-date, so append it.
	canonicalHeaders := fmt.Sprintf("content-type:application/x-www-form-urlencoded\nhost:%s\nx-amz-date:%s\n", host, amzDate)
	signedHeaders := "content-type;host;x-amz-date"
	if p.creds.SessionToken != "" {
		canonicalHeaders += "x-amz-security-token:" + p.creds.SessionToken + "\n"
		signedHeaders += ";x-amz-security-token"
	}
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
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read aws ec2 response: %w", err)
	}
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
