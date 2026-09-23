package monitoring

import "testing"

func TestMemorySnapshotAccounting(t *testing.T) {
	info := &ProcMemInfo{MemTotal: 1000, MemFree: 100, Cached: 200, SReclaimable: 40, Buffers: 30, Shmem: 20, SwapTotal: 300, SwapFree: 100, SwapCached: 20}
	if got := ramFromProc(info, false); got.Total != 1000 || got.Used != 650 || got.Mode != "htoplike" {
		t.Fatalf("htop accounting: %+v", got)
	}
	if got := ramFromProc(info, true); got.Used != 900 || got.Mode != "includeCache" {
		t.Fatalf("include cache: %+v", got)
	}
	if got := swapFromProc(info); got.Total != 300 || got.Used != 180 {
		t.Fatalf("swap accounting: %+v", got)
	}
	info.Cached = 2000
	info.SwapCached = 500
	if got := ramFromProc(info, false); got.Used != 920 {
		t.Fatalf("overlapping cache fallback: %+v", got)
	}
	if got := swapFromProc(info); got.Used != 200 {
		t.Fatalf("swap cache fallback: %+v", got)
	}
}
