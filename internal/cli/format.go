package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/memohnsen/gads-cli/internal/api"
)

func validFormat(f string) bool { return f == "json" || f == "csv" || f == "table" }

// formatReport turns one search or search-stream response into csv or a
// table: the rows are search's "results", or every batch's "results" for
// search-stream, which answers with an array of batches.
func formatReport(body []byte, format string) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	var rows []json.RawMessage
	switch {
	case len(trimmed) > 0 && trimmed[0] == '[':
		var batches []struct {
			Results []json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(trimmed, &batches); err != nil {
			return nil, api.Usagef("--format %s: unreadable search-stream response: %v", format, err)
		}
		for _, b := range batches {
			rows = append(rows, b.Results...)
		}
	default:
		var page struct {
			Results []json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(trimmed, &page); err != nil {
			return nil, api.Usagef("--format %s: unreadable search response: %v", format, err)
		}
		rows = page.Results
	}
	return formatRows(rows, format)
}

// formatRows flattens each GAQL row into dotted columns (campaign.id,
// metrics.clicks) in the order Google sent the fields, and writes them as
// csv or an aligned table. Repeated fields stay JSON in their cell.
func formatRows(rows []json.RawMessage, format string) ([]byte, error) {
	var cols []string
	seen := map[string]bool{}
	flat := make([]map[string]string, 0, len(rows))
	for i, r := range rows {
		m := map[string]string{}
		dec := json.NewDecoder(bytes.NewReader(r))
		dec.UseNumber()
		if err := flatten(dec, "", m, func(k string) {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}); err != nil {
			return nil, api.Usagef("--format %s: row %d: %v", format, i, err)
		}
		flat = append(flat, m)
	}
	var buf bytes.Buffer
	switch format {
	case "csv":
		w := csv.NewWriter(&buf)
		w.Write(cols)
		for _, m := range flat {
			rec := make([]string, len(cols))
			for j, c := range cols {
				rec[j] = m[c]
			}
			w.Write(rec)
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, api.Usagef("--format csv: %v", err)
		}
	case "table":
		w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, strings.Join(cols, "\t"))
		for _, m := range flat {
			rec := make([]string, len(cols))
			for j, c := range cols {
				rec[j] = strings.NewReplacer("\t", " ", "\n", " ").Replace(m[c])
			}
			fmt.Fprintln(w, strings.Join(rec, "\t"))
		}
		w.Flush()
	default:
		return nil, api.Usagef("--format wants json, csv or table, got %q", format)
	}
	return buf.Bytes(), nil
}

// flatten reads one JSON value from dec, keeping object key order, and
// stores scalars under dotted paths. Arrays are kept as compact JSON.
func flatten(dec *json.Decoder, prefix string, out map[string]string, col func(string)) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				key := kt.(string)
				if prefix != "" {
					key = prefix + "." + key
				}
				if err := flatten(dec, key, out, col); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		case '[':
			var items []json.RawMessage
			for dec.More() {
				var raw json.RawMessage
				if err := dec.Decode(&raw); err != nil {
					return err
				}
				items = append(items, raw)
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			b, _ := json.Marshal(items)
			col(prefix)
			out[prefix] = string(b)
			return nil
		}
	case nil:
		col(prefix)
		out[prefix] = ""
	case string:
		col(prefix)
		out[prefix] = t
	case json.Number:
		col(prefix)
		out[prefix] = t.String()
	case bool:
		col(prefix)
		out[prefix] = fmt.Sprint(t)
	default:
		return fmt.Errorf("unexpected %v", t)
	}
	return nil
}

