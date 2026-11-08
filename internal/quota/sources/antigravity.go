package sources

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/furbyhaxx/pig-auth-plus/internal/providers/antigravity"
	"github.com/furbyhaxx/pig-auth-plus/internal/quota"
)

// Antigravity reports every quota bucket Cloud Code returns. projects discovers the Cloud
// Code project per credential; it is shared with the inference provider.
func Antigravity(projects *antigravity.Projects) *quota.Source {
	return &quota.Source{
		ID: "antigravity", Label: "Antigravity", Short: "ag", Aliases: []string{"agy", "ag"},
		ParserVersion: 2, MinPoll: time.Minute,
		Fetch: func(ctx context.Context, env quota.Env) (quota.Snapshot, error) {
			auth, err := env.Auth(ctx)
			if err != nil {
				return quota.Snapshot{}, err
			}
			if auth == nil {
				return needsAuth("antigravity", env.Now(), "antigravity: no login; run /login antigravity"), nil
			}
			project, err := projects.Resolve(ctx, auth.Token)
			if err != nil {
				return quota.Snapshot{}, err
			}
			base := projects.BaseURL
			if base == "" {
				base = antigravity.BaseURL
			}
			body, err := getJSON(ctx, "antigravity", http.MethodPost, base+"/v1internal:retrieveUserQuotaSummary",
				antigravity.CLIHeaders(auth.Token), []byte(fmt.Sprintf(`{"project":%q}`, project)), auth.Token, project)
			if err != nil {
				return quota.Snapshot{}, err
			}
			return ParseAntigravity(body, env.Now())
		},
	}
}

func agGroupTag(id, name string) string {
	switch {
	case strings.HasPrefix(id, "gemini-argon"):
		return "arg"
	case strings.HasPrefix(id, "gemini-"):
		return "gem"
	case strings.HasPrefix(id, "3p-"):
		return "3p"
	}
	if name == "" {
		name = "pool"
	}
	return strings.ToLower(trunc(name, 3))
}

func agUsed(b map[string]any) *float64 {
	r := num(b["remainingFraction"])
	if r == nil {
		return nil
	}
	return quota.Frac(1 - *frac(r))
}

func missing(field string) error {
	return fmt.Errorf("antigravity: quota response missing %s; retry later or update pig-auth-plus", field)
}

// ParseAntigravity reports windows without inferring usage or exposing the project as a tier.
func ParseAntigravity(body map[string]any, now time.Time) (quota.Snapshot, error) {
	var ws []quota.Window
	seen := map[string]bool{}
	appendGroup := func(group map[string]any) error {
		raw, ok := group["buckets"].([]any)
		if !ok {
			return missing("group buckets")
		}
		buckets := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			b := obj(r)
			if text(b["bucketId"]) == "" {
				return missing("bucketId")
			}
			buckets = append(buckets, b)
		}
		name := text(group["displayName"])
		tag := ""
		if len(buckets) > 0 {
			tag = agGroupTag(text(buckets[0]["bucketId"]), name)
		}
		maxIdx, maxUsed := -1, -1.0
		for i, b := range buckets {
			if u := agUsed(b); u != nil && *u > maxUsed {
				maxIdx, maxUsed = i, *u
			}
		}
		for i, b := range buckets {
			id := text(b["bucketId"])
			if seen[id] {
				continue
			}
			seen[id] = true
			period := text(b["window"])
			if period == "weekly" {
				period = "7d"
			}
			important := i == maxIdx
			short := tag
			if !important && len(buckets) != 1 {
				short = tag + ":" + period
			}
			label := name
			if label == "" {
				label = tag
			}
			detail := period
			if detail == "" {
				detail = text(b["displayName"])
			}
			if detail == "" {
				detail = "limit"
			}
			scope := ""
			switch {
			case strings.HasPrefix(id, "gemini-argon-"):
				scope = "gemini-4-argon"
			case strings.HasPrefix(id, "gemini-"):
				scope = "gemini"
			case strings.HasPrefix(id, "3p-"):
				scope = "3p"
			default:
				scope = strings.ToLower(firstNonEmpty(name, text(b["displayName"]), tag))
			}
			w := quota.Window{ID: id, Short: short, Label: fmt.Sprintf("%s (%s)", label, detail), Used: agUsed(b),
				ResetAtMs: timeMs(b["resetTime"]), Important: important, Group: name, Period: period, Scope: scope}
			ws = append(ws, w)
		}
		return nil
	}
	if body == nil {
		return quota.Snapshot{}, missing("quota groups or buckets")
	}
	if groups, ok := body["groups"].([]any); ok {
		for _, g := range groups {
			gm := obj(g)
			if gm == nil {
				return quota.Snapshot{}, missing("quota group object")
			}
			if err := appendGroup(gm); err != nil {
				return quota.Snapshot{}, err
			}
		}
	}
	if flat, ok := body["buckets"].([]any); ok {
		var order []string
		byTag := map[string][]any{}
		for _, r := range flat {
			b := obj(r)
			if b == nil {
				return quota.Snapshot{}, missing("quota bucket object")
			}
			id := text(b["bucketId"])
			if id == "" {
				return quota.Snapshot{}, missing("bucketId")
			}
			if seen[id] {
				continue
			}
			tag := agGroupTag(id, "")
			if _, ok := byTag[tag]; !ok {
				order = append(order, tag)
			}
			byTag[tag] = append(byTag[tag], b)
		}
		for _, tag := range order {
			if err := appendGroup(map[string]any{"buckets": byTag[tag]}); err != nil {
				return quota.Snapshot{}, err
			}
		}
	}
	if len(ws) == 0 {
		return quota.Snapshot{}, missing("quota buckets")
	}
	return quota.Snapshot{Provider: "antigravity", FetchedAt: now.UnixMilli(), Source: quota.FromEndpoint, Windows: ws}, nil
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}
