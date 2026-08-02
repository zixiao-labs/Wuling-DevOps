package githubapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CreateCheckRunRequest is the GitHub Checks API create payload (subset).
type CreateCheckRunRequest struct {
	Name        string `json:"name"`
	HeadSHA     string `json:"head_sha"`
	Status      string `json:"status,omitempty"`
	Conclusion  string `json:"conclusion,omitempty"`
	DetailsURL  string `json:"details_url,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	Output      *CheckOutput `json:"output,omitempty"`
}

// CheckOutput is the check run summary block.
type CheckOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Text    string `json:"text,omitempty"`
}

// UpdateCheckRunRequest patches status/conclusion/output.
type UpdateCheckRunRequest struct {
	Status     string       `json:"status,omitempty"`
	Conclusion string       `json:"conclusion,omitempty"`
	Output     *CheckOutput `json:"output,omitempty"`
	DetailsURL string       `json:"details_url,omitempty"`
}

// CreateCheckRun creates a check run on owner/repo.
func (c *Client) CreateCheckRun(token, owner, repo string, body CreateCheckRunRequest) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.doJSON(token, http.MethodPost,
		fmt.Sprintf("%s/repos/%s/%s/check-runs", apiBase, owner, repo), body, &out)
	return out.ID, err
}

// UpdateCheckRun patches an existing check run.
func (c *Client) UpdateCheckRun(token, owner, repo string, checkRunID int64, body UpdateCheckRunRequest) error {
	return c.doJSON(token, http.MethodPatch,
		fmt.Sprintf("%s/repos/%s/%s/check-runs/%d", apiBase, owner, repo, checkRunID), body, nil)
}

func (c *Client) doJSON(token, method, url string, in, out any) error {
	var rdr io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("github %s %s -> %d", method, url, res.StatusCode)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// MapConclusion maps Wuling job/run status to a GitHub check conclusion.
func MapConclusion(status string) string {
	switch status {
	case "success":
		return "success"
	case "failed":
		return "failure"
	case "canceled", "cancelled":
		return "cancelled"
	default:
		return "neutral"
	}
}
