package cli

import (
	"bytes"
	"encoding/json"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/wir-drei-digital/google-ads-cli/internal/api"
	"github.com/wir-drei-digital/google-ads-cli/internal/manifest"
)

// runAll follows page tokens and writes ONE JSON array of the merged rows.
// It is the only place the CLI reshapes a response, so each row reaches the
// output exactly as Google sent it. Hitting --max-pages is not silent
// success: the partial array is written and the run ends as incomplete.
func (a *app) runAll(cmd *cobra.Command, meth *manifest.Method, base api.Request, out *outputFile) error {
	maxPages, _ := cmd.Flags().GetInt("max-pages")
	if maxPages < 1 {
		return api.Usagef("--max-pages must be at least 1, got %d", maxPages)
	}
	var body map[string]any
	if meth.Paging == manifest.PagingBodyToken {
		body = map[string]any{}
		if len(base.Body) > 0 {
			dec := json.NewDecoder(bytes.NewReader(base.Body))
			dec.UseNumber()
			if err := dec.Decode(&body); err != nil {
				return api.Usagef("--all needs a JSON object body: %v", err)
			}
		}
		if wantsSummaryRow(body) {
			return api.Usagef("--all merges rows only and would drop the summary row; page by hand or use search-stream when you need searchSettings.returnSummaryRow")
		}
		// A page token in --data would start the walk mid-stream and drop
		// the pages before it, so the walk always starts at the first page.
		delete(body, "pageToken")
		delete(body, "page_token")
	}
	var rows []json.RawMessage
	token, complete := "", false
	for page := 0; page < maxPages; page++ {
		req := base
		switch meth.Paging {
		case manifest.PagingBodyToken:
			if token != "" {
				body["pageToken"] = token
			}
			raw, err := jsonCompact(body)
			if err != nil {
				return api.Usagef("%v", err)
			}
			req.Body = raw
		case manifest.PagingQueryToken:
			q := url.Values{}
			for k, v := range base.Query {
				q[k] = append([]string{}, v...)
			}
			if token != "" {
				q.Set("pageToken", token)
			}
			req.Query = q
		}
		resp, err := a.client.Do(cmd.Context(), req)
		if err != nil {
			return err
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(resp.Body, &env); err != nil {
			return api.Usagef("--all expects a JSON object response, got: %.100s", resp.Body)
		}
		var pageRows []json.RawMessage
		if raw, ok := env[meth.ResultsField]; ok {
			if err := json.Unmarshal(raw, &pageRows); err != nil {
				return api.Usagef("--all: %s is not an array", meth.ResultsField)
			}
		}
		rows = append(rows, pageRows...)
		token = ""
		if raw, ok := env["nextPageToken"]; ok {
			json.Unmarshal(raw, &token)
		}
		if token == "" {
			complete = true
			break
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, r := range rows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(r)
	}
	buf.WriteString("]\n")
	if err := a.writeResponse(&api.Response{Body: buf.Bytes()}, out); err != nil {
		return err
	}
	if !complete {
		return &api.Error{Kind: api.KindIncomplete, Message: "hit --max-pages before the last page; the output is partial"}
	}
	return nil
}

func wantsSummaryRow(body map[string]any) bool {
	for _, key := range []string{"searchSettings", "search_settings"} {
		if ss, ok := body[key].(map[string]any); ok {
			for _, f := range []string{"returnSummaryRow", "return_summary_row"} {
				if v, _ := ss[f].(bool); v {
					return true
				}
			}
		}
	}
	return false
}
