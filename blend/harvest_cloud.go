package blend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// awsReads is the number of independent reads one harvest performs.
const awsReads = 5

// AWSRunner runs one AWS CLI invocation and returns its stdout. The caller owns
// credentials, region and profile; HarvestAWS only ever issues list/describe
// reads.
type AWSRunner func(ctx context.Context, args ...string) ([]byte, error)

const awsOutputFlag = "--output"

// HarvestAWS reads IAM and EC2 naming conventions. Each call is independent: a
// failed read contributes nothing and the remaining reads still run. The error
// is non-nil only when every read failed.
func HarvestAWS(ctx context.Context, run AWSRunner, account string) ([]Observation, error) {
	if run == nil {
		return nil, errors.New("blend: nil AWSRunner")
	}
	account = strings.TrimSpace(account)
	prefix := "aws"
	if account != "" {
		prefix += ":" + account
	}
	out := newCollector()
	attempted, failures := 0, 0

	read := func(label string, args []string, dst any) bool {
		attempted++
		body, err := run(ctx, args...)
		if err != nil {
			failures++
			out.fail(fmt.Errorf("%s: %w", label, err))
			return false
		}
		if err := json.Unmarshal(body, dst); err != nil {
			failures++
			out.fail(fmt.Errorf("%s: decode: %w", label, err))
			return false
		}
		return true
	}

	var users struct {
		Users []struct {
			UserName string `json:"UserName"`
		} `json:"Users"`
	}
	if read("iam:list-users", []string{"iam", "list-users", awsOutputFlag, "json"}, &users) {
		for _, user := range users.Users {
			out.add(ClassIAMUser, user.UserName, prefix+":iam:list-users")
		}
	}

	var roles struct {
		Roles []struct {
			RoleName string `json:"RoleName"`
		} `json:"Roles"`
	}
	if read("iam:list-roles", []string{"iam", "list-roles", awsOutputFlag, "json"}, &roles) {
		for _, role := range roles.Roles {
			out.add(ClassIAMRole, role.RoleName, prefix+":iam:list-roles")
		}
	}

	var profiles struct {
		InstanceProfiles []struct {
			InstanceProfileName string `json:"InstanceProfileName"`
		} `json:"InstanceProfiles"`
	}
	if read("iam:list-instance-profiles", []string{"iam", "list-instance-profiles", awsOutputFlag, "json"}, &profiles) {
		for _, profile := range profiles.InstanceProfiles {
			out.add(ClassIAMRole, profile.InstanceProfileName, prefix+":iam:list-instance-profiles")
		}
	}

	var tags struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if read("ec2:describe-tags", []string{"ec2", "describe-tags", awsOutputFlag, "json"}, &tags) {
		for _, tag := range tags.Tags {
			switch tag.Key {
			case "Name", "Project", "Environment":
				out.add(ClassTagValue, tag.Value, prefix+":ec2:describe-tags:"+tag.Key)
			}
		}
	}

	var groups struct {
		SecurityGroups []struct {
			GroupName   string `json:"GroupName"`
			Description string `json:"Description"`
		} `json:"SecurityGroups"`
	}
	if read("ec2:describe-security-groups", []string{"ec2", "describe-security-groups", awsOutputFlag, "json"}, &groups) {
		for _, group := range groups.SecurityGroups {
			out.add(ClassSGName, group.GroupName, prefix+":ec2:describe-security-groups")
			out.add(ClassSGDescription, group.Description, prefix+":ec2:describe-security-groups")
		}
	}

	if failures == attempted {
		return out.obs, errors.Join(out.errs...)
	}
	return out.obs, nil
}

// HarvestUA reads identity-revealing response headers. The contract has no
// dedicated class, so they are recorded as markers with a header source.
func HarvestUA(headers http.Header) []Observation {
	if headers == nil {
		return nil
	}
	out := newCollector()
	for _, name := range []string{"User-Agent", "Server", "X-Powered-By"} {
		for _, value := range headers.Values(name) {
			out.add(ClassMarker, value, "http-header:"+strings.ToLower(name))
		}
	}
	return out.obs
}
