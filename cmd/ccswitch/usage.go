package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// usageEndpoint is what Claude Code's own /usage screen reads. It is not a
// documented API, so everything here is display-only and fails silently: we
// send the profile's existing token, show the numbers if the response parses,
// and show nothing at all if the shape ever changes. Nothing is ever written
// back and tokens are never refreshed from here.
const usageEndpoint = "https://api.anthropic.com/api/oauth/usage"

// UsageWindow is one rate-limit window.
type UsageWindow struct {
	Utilization float64   // 0–100, percent of the window consumed
	ResetsAt    time.Time // when the window rolls over; may be zero
}

// Usage is a profile's current rate-limit standing.
type Usage struct {
	Session *UsageWindow // rolling 5-hour window
	Week    *UsageWindow // rolling 7-day window, all models
	Opus    *UsageWindow // rolling 7-day window, Opus only
}

type wireWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type wireUsage struct {
	FiveHour     *wireWindow `json:"five_hour"`
	SevenDay     *wireWindow `json:"seven_day"`
	SevenDayOpus *wireWindow `json:"seven_day_opus"`
}

func (w *wireWindow) window() *UsageWindow {
	if w == nil {
		return nil
	}
	out := &UsageWindow{Utilization: w.Utilization}
	if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
		out.ResetsAt = t
	}
	return out
}

// FetchUsage asks Anthropic how much of the profile's rate limits are used.
// A profile whose access token has gone stale gets an error rather than a
// refresh attempt — launching the profile once lets Claude Code refresh it.
func FetchUsage(p Profile) (*Usage, error) {
	cr, err := readCredentials(p.Dir)
	if err != nil || cr.ClaudeAiOauth.AccessToken == "" {
		return nil, fmt.Errorf("not signed in")
	}
	o := cr.ClaudeAiOauth
	if o.ExpiresAt > 0 && time.Now().After(time.UnixMilli(o.ExpiresAt)) {
		return nil, fmt.Errorf("token needs refresh — launch this profile once")
	}

	req, err := http.NewRequest("GET", usageEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ccswitch/"+version)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage unavailable (HTTP %d)", resp.StatusCode)
	}

	var wu wireUsage
	if err := json.NewDecoder(resp.Body).Decode(&wu); err != nil {
		return nil, fmt.Errorf("usage unavailable")
	}
	u := &Usage{
		Session: wu.FiveHour.window(),
		Week:    wu.SevenDay.window(),
		Opus:    wu.SevenDayOpus.window(),
	}
	if u.Session == nil && u.Week == nil {
		return nil, fmt.Errorf("usage unavailable")
	}
	return u, nil
}

// resetClock renders a reset time compactly: clock only when it's within a
// day, weekday otherwise.
func resetClock(t time.Time) string {
	t = t.Local()
	if time.Until(t) < 24*time.Hour {
		return t.Format("3:04 PM")
	}
	return t.Format("Mon 3 PM")
}

func pct(v float64) int {
	return int(v + 0.5)
}

// cmdUsage prints every profile's rate-limit standing, fetching them all
// concurrently so ten profiles cost one round-trip, not ten. With --watch it
// hands over to the live panel in watch.go instead.
func cmdUsage(args []string) error {
	watch := false
	every := watchDefaultEvery

	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func(flag string) (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			return args[i], nil
		}
		var raw string
		var err error
		switch {
		case a == "--watch" || a == "-w":
			watch = true
		case a == "--every":
			raw, err = next("--every")
		case strings.HasPrefix(a, "--every="):
			raw = strings.TrimPrefix(a, "--every=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (usage: ccswitch usage [--watch] [--every 60s])", a)
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
		if err != nil {
			return err
		}
		if raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("--every wants a duration like 30s or 2m: %w", err)
			}
			// The endpoint is undocumented and shared with Claude Code itself;
			// polling it harder than this is rude and tells you nothing new.
			if d < watchMinEvery {
				return fmt.Errorf("--every is capped at %s minimum", watchMinEvery)
			}
			every = d
			watch = true
		}
	}

	if watch {
		return runUsageWatch(every)
	}

	ps, err := List()
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Println("no profiles yet — run ccswitch and press i or n")
		return nil
	}

	results := make([]string, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		if !p.SignedIn {
			results[i] = "not signed in"
			continue
		}
		wg.Add(1)
		go func(i int, p Profile) {
			defer wg.Done()
			u, err := FetchUsage(p)
			if err != nil {
				results[i] = err.Error()
				return
			}
			results[i] = usageText(u)
		}(i, p)
	}
	wg.Wait()

	for i, p := range ps {
		fmt.Printf("%-18s %-30s %s\n", p.Name, p.Label(), results[i])
	}
	return nil
}

func usageText(u *Usage) string {
	var parts []string
	if w := u.Session; w != nil {
		s := fmt.Sprintf("5h %d%%", pct(w.Utilization))
		if !w.ResetsAt.IsZero() {
			s += " (resets " + resetClock(w.ResetsAt) + ")"
		}
		parts = append(parts, s)
	}
	if w := u.Week; w != nil {
		s := fmt.Sprintf("7d %d%%", pct(w.Utilization))
		if !w.ResetsAt.IsZero() {
			s += " (resets " + resetClock(w.ResetsAt) + ")"
		}
		parts = append(parts, s)
	}
	if w := u.Opus; w != nil && w.Utilization > 0 {
		parts = append(parts, fmt.Sprintf("opus %d%%", pct(w.Utilization)))
	}
	return strings.Join(parts, " · ")
}
