package quota

import "testing"

func win(at int64, v float64, reset int64) Snapshot {
	var r *int64
	if reset != 0 {
		r = Ms(reset)
	}
	return Snapshot{Provider: "p", FetchedAt: at, Windows: []Window{{ID: "5h", Used: Frac(v), ResetAtMs: r}}}
}

func record(h []History, s Snapshot) []History { return Record(h, s, s.FetchedAt) }

func TestRateNeedsSixtySecondsOfCoverage(t *testing.T) {
	h := record(nil, win(0+1000, 0.10, 0))
	h = record(h, win(1000+30_000, 0.12, 0))
	if m := Compute(&h[0], 40_000); m.Window != nil {
		t.Fatalf("30 s of coverage produced a rate: %+v", m.Window)
	}
	h = record(h, win(1000+3_600_000, 0.20, 0))
	m := Compute(&h[0], 3_700_000)
	if m.Window == nil || m.Window.PerHour < 0.099 || m.Window.PerHour > 0.101 {
		t.Fatalf("window rate = %+v", m.Window)
	}
	if m.ETASec == nil || *m.ETASec < 28_700 || *m.ETASec > 28_900 {
		t.Fatalf("eta = %v, want ~8 h", m.ETASec)
	}
}

func TestUtilizationDropAndResetAdvanceStartNewSegments(t *testing.T) {
	h := record(nil, win(1000, 0.5, 100_000))
	h = record(h, win(120_000, 0.6, 100_000))
	h = record(h, win(240_000, 0.1, 100_000)) // utilization drop
	if len(h[0].Samples) != 1 || h[0].Samples[0].Value != 0.1 {
		t.Fatalf("drop did not restart the segment: %+v", h[0].Samples)
	}
	h = record(h, win(360_000, 0.2, 100_000))
	h = record(h, win(480_000, 0.3, 100_000+3_600_000)) // reset time advanced an hour
	if len(h[0].Samples) != 1 {
		t.Fatalf("reset advance did not restart the segment: %+v", h[0].Samples)
	}
}

func TestBalanceDrainsDownAndTopUpRestarts(t *testing.T) {
	bal := func(at int64, rem float64) Snapshot {
		return Snapshot{FetchedAt: at, Credits: &Balance{Currency: "$", Remaining: Frac(rem)}}
	}
	h := record(nil, bal(1000, 10))
	h = record(h, bal(3_601_000, 8))
	m := Compute(&h[0], 3_602_000)
	if m.Window == nil || m.Window.PerHour != 2 {
		t.Fatalf("balance rate = %+v", m.Window)
	}
	h = record(h, bal(7_201_000, 50)) // top-up
	if len(h[0].Samples) != 1 {
		t.Fatalf("top-up did not restart: %+v", h[0].Samples)
	}
}

func TestResetBeforeExhaustionAndPassedReset(t *testing.T) {
	h := record(nil, win(1000, 0.1, 5_000_000))
	h = record(h, win(3_601_000, 0.2, 5_000_000))
	m := Compute(&h[0], 3_700_000)
	if !m.ResetsBeforeExhaustion || m.ETASec != nil {
		t.Fatalf("reset arrives in ~23 min, before the 8 h ETA: %+v", m)
	}
	m = Compute(&h[0], 6_000_000) // reset time has passed with no newer observation
	if m.ETASec != nil || m.ResetsBeforeExhaustion || m.Window == nil {
		t.Fatalf("passed reset must suppress the ETA but keep rates: %+v", m)
	}
}

func TestFailedAndStaleSnapshotsAreNotRecorded(t *testing.T) {
	h := record(nil, win(5000, 0.1, 0))
	failed := win(9000, 0.9, 0)
	failed.Error = "boom"
	h = record(h, failed)
	h = record(h, win(4000, 0.05, 0)) // older than the last sample
	if len(h[0].Samples) != 1 || h[0].Samples[0].Value != 0.1 {
		t.Fatalf("samples = %+v", h[0].Samples)
	}
}
