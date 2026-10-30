package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// memoryCounts is what a tab keeps alive once its garbage is collected, as Chrome counts it: a
// screen left and opened again leaves them where they were, and counts that grow with every
// round are a leak.
type memoryCounts struct {
	Nodes     int64 `json:"nodes"`
	Listeners int64 `json:"listeners"`
	Heap      int64 `json:"heap"`
	Documents int64 `json:"documents"`
}

// readMemory collects the tab's garbage and reads what it still holds.
func readMemory(page *rod.Page) (memoryCounts, error) {
	// twice: what a first collection frees can let a second free more
	for range 2 {
		if err := (proto.HeapProfilerCollectGarbage{}).Call(page); err != nil {
			return memoryCounts{}, fmt.Errorf("collect the garbage: %w", err)
		}
	}
	if err := (proto.PerformanceEnable{}).Call(page); err != nil {
		return memoryCounts{}, fmt.Errorf("read the tab's counts: %w", err)
	}
	got, err := proto.PerformanceGetMetrics{}.Call(page)
	if err != nil {
		return memoryCounts{}, fmt.Errorf("read the tab's counts: %w", err)
	}
	counts := map[string]float64{}
	for _, m := range got.Metrics {
		counts[m.Name] = m.Value
	}
	return memoryCounts{
		Nodes:     int64(counts["Nodes"]),
		Listeners: int64(counts["JSEventListeners"]),
		Heap:      int64(counts["JSHeapUsedSize"]),
		Documents: int64(counts["Documents"]),
	}, nil
}

// markPath is where a port's Chrome keeps its mark (memory mark): in its own profile, so it
// holds from one command to the next and goes with the browser.
func markPath(port int) string {
	return filepath.Join(profileDir(port), "memory-mark.json")
}

// cmdMemory prints what the tab keeps alive; "memory mark" also keeps it as the mark that
// "assert memory" measures growth from.
func cmdMemory(ctx *cmdContext, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "mark") {
		return fmt.Errorf("usage: memory [mark]")
	}
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}
	counts, err := readMemory(page)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		data, err := json.Marshal(counts)
		if err != nil {
			return err
		}
		// a vault's Chrome has its profile elsewhere: the mark's folder is made when missing
		if err := os.MkdirAll(filepath.Dir(markPath(ctx.port)), 0o700); err != nil {
			return fmt.Errorf("keep the mark: %w", err)
		}
		if err := os.WriteFile(markPath(ctx.port), data, 0o600); err != nil {
			return fmt.Errorf("keep the mark: %w", err)
		}
	}
	fmt.Printf("nodes %d listeners %d heap %d documents %d\n", counts.Nodes, counts.Listeners,
		counts.Heap, counts.Documents)
	return nil
}

// assertMemory fails when what the tab keeps alive grew since the mark by more than the limits
// it is given, as pairs: "nodes 30 listeners 10" (nodes, listeners, heap in bytes, documents).
func assertMemory(ctx *cmdContext, args []string) error {
	usage := fmt.Errorf("usage: assert memory <nodes|listeners|heap|documents> <max growth> ...")
	if len(args) == 0 || len(args)%2 != 0 {
		return usage
	}
	data, err := os.ReadFile(markPath(ctx.port))
	if err != nil {
		return fmt.Errorf("assert memory: no mark to measure from — run \"memory mark\" first")
	}
	var mark memoryCounts
	if err := json.Unmarshal(data, &mark); err != nil {
		return fmt.Errorf("assert memory: the mark is not readable: %w", err)
	}
	_, page, err := connect(ctx)
	if err != nil {
		return err
	}
	now, err := readMemory(page)
	if err != nil {
		return err
	}
	for i := 0; i < len(args); i += 2 {
		limit, err := strconv.ParseInt(args[i+1], 10, 64)
		if err != nil || limit < 0 {
			return fmt.Errorf("assert memory: %q is not a growth (a whole number)", args[i+1])
		}
		var before, after int64
		switch args[i] {
		case "nodes":
			before, after = mark.Nodes, now.Nodes
		case "listeners":
			before, after = mark.Listeners, now.Listeners
		case "heap":
			before, after = mark.Heap, now.Heap
		case "documents":
			before, after = mark.Documents, now.Documents
		default:
			return usage
		}
		if after-before > limit {
			return fmt.Errorf("assert memory: %s grew %d (%d → %d), more than %d", args[i],
				after-before, before, after, limit)
		}
	}
	return nil
}
