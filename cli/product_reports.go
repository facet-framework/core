// @facet: navigation/interfaces/product-services
// @facet-role: primary
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/facet-framework/core/registry"
	"io"
	"sort"
	"strings"
	"time"
)

type productComparison struct {
	Registries []string       `json:"registries"`
	Baseline   facet.Baseline `json:"baseline"`
}

func comparisonInput(regs []facet.Registry, baseline facet.Baseline) productComparison {
	names := []string{}
	for _, r := range regs {
		names = append(names, r.Name)
	}
	return productComparison{names, baseline}
}
func writeProductDriftReport(w io.Writer, root string, regs []facet.Registry, report facet.DriftReport, asJSON bool) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	texts := []string{}
	failures := map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, reply := range invokeProducts(ctx, root, productView(regs), "drift-report", comparisonInput(regs, report.Baseline)) {
		var result struct {
			Fields map[string]json.RawMessage `json:"fields"`
			Text   string                     `json:"text"`
		}
		err := reply.Err
		if err == nil {
			err = json.Unmarshal(reply.Payload, &result)
		}
		if err == nil {
			for key := range result.Fields {
				if _, exists := fields[key]; exists {
					err = fmt.Errorf("product cannot replace drift field %q", key)
					break
				}
			}
		}
		if err != nil {
			failures[reply.ID] = err.Error()
			continue
		}
		for key, value := range result.Fields {
			fields[key] = value
		}
		if result.Text != "" {
			texts = append(texts, result.Text)
		}
	}
	if len(failures) > 0 {
		fields["product_errors"], _ = json.Marshal(failures)
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(fields)
	}
	printDrift(w, report)
	for _, text := range texts {
		fmt.Fprint(w, text)
	}
	keys := []string{}
	for id := range failures {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		fmt.Fprintf(w, "\nproduct %s: could not be checked — %s\n", id, failures[id])
	}
	return nil
}
func productStopNotices(c hookContext, session string, baseline facet.Baseline) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := []string{}
	for _, reply := range invokeProducts(ctx, c.root, productView(c.registries), "stop-notices", comparisonInput(c.registries, baseline)) {
		var result struct {
			Groups []struct {
				Header  string `json:"header"`
				Footer  string `json:"footer"`
				Notices []struct {
					Key  string `json:"key"`
					Text string `json:"text"`
				} `json:"notices"`
			} `json:"groups"`
		}
		err := reply.Err
		if err == nil {
			err = json.Unmarshal(reply.Payload, &result)
		}
		if err != nil {
			if noticeIsNew(session, "product-error:"+reply.ID+":"+c.root) {
				out = append(out, fmt.Sprintf("FACET product %s: review could not be checked (%v)", reply.ID, err))
			}
			continue
		}
		for _, g := range result.Groups {
			lines := []string{}
			for _, n := range g.Notices {
				if n.Key != "" && noticeIsNew(session, "product:"+reply.ID+":"+c.root+":"+n.Key) {
					lines = append(lines, n.Text)
				}
			}
			if len(lines) > 0 {
				sort.Strings(lines)
				out = append(out, g.Header+"\n\n"+strings.Join(lines, "\n")+"\n\n"+g.Footer)
			}
		}
	}
	return out
}
