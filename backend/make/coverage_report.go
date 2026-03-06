//go:build ignore

// coverage_report parses a Go coverage profile and prints a per-file,
// per-function table sorted by coverage ascending (worst files first).
// Files at 100% are omitted — they need no action.
//
// Usage:
//
//	go run make/coverage_report.go <coverage.out> <output.txt> [module-prefix] [exclude-prefix,...]
//
// Examples:
//
//	go run make/coverage_report.go coverage.out uncovered.txt github.com/my/mod/
//	go run make/coverage_report.go coverage.out uncovered.txt github.com/my/mod/ internal/db,internal/mock
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// ANSI colour codes — only used for terminal output, never written to file.
const (
	colRed    = "\033[31m"
	colYellow = "\033[33m"
	colCyan   = "\033[36m"
	colBold   = "\033[1m"
	colReset  = "\033[0m"
)

type fn struct {
	line int
	name string
	pct  float64
}

type file struct {
	path string
	avg  float64
	fns  []fn
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: coverage_report.go <coverage.out> <output.txt> [module-prefix] [exclude-prefix,...]")
		os.Exit(1)
	}

	coverFile := os.Args[1]
	outFile := os.Args[2]

	modPrefix := ""
	if len(os.Args) >= 4 {
		modPrefix = os.Args[3]
	}

	// Comma-separated path prefixes to exclude from the report.
	// e.g. "internal/db,internal/mock"
	var excludes []string
	if len(os.Args) >= 5 {
		for _, e := range strings.Split(os.Args[4], ",") {
			if t := strings.TrimSpace(e); t != "" {
				excludes = append(excludes, t)
			}
		}
	}

	// ── Run go tool cover -func ───────────────────────────────────────────────
	raw, err := exec.Command("go", "tool", "cover", "-func="+coverFile).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go tool cover failed: %v\n", err)
		os.Exit(1)
	}

	// ── Parse output ──────────────────────────────────────────────────────────
	// Each non-total line looks like (tab-separated, variable whitespace):
	//   github.com/.../handler.go:42:   Handle   0.0%
	fileMap := make(map[string]*file)
	var fileOrder []string
	totalStr := "n/a"

	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "total:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				totalStr = fields[len(fields)-1]
			}
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		col := fields[0]                                         // "pkg/file.go:42:"
		fnName := fields[1]                                      // "FuncName"
		pctStr := strings.TrimSuffix(fields[len(fields)-1], "%") // "0.0"
		pct, _ := strconv.ParseFloat(pctStr, 64)

		parts := strings.SplitN(col, ":", 3)
		if len(parts) < 2 {
			continue
		}
		filePath := strings.TrimPrefix(parts[0], modPrefix)
		lineNum, _ := strconv.Atoi(parts[1])

		// Skip excluded prefixes
		if isExcluded(filePath, excludes) {
			continue
		}

		if _, exists := fileMap[filePath]; !exists {
			fileMap[filePath] = &file{path: filePath}
			fileOrder = append(fileOrder, filePath)
		}
		fileMap[filePath].fns = append(fileMap[filePath].fns, fn{line: lineNum, name: fnName, pct: pct})
	}

	// ── Compute per-file averages ─────────────────────────────────────────────
	for _, k := range fileOrder {
		fe := fileMap[k]
		if len(fe.fns) == 0 {
			continue
		}
		sum := 0.0
		for _, f := range fe.fns {
			sum += f.pct
		}
		fe.avg = sum / float64(len(fe.fns))
	}

	// ── Sort files: lowest average first ─────────────────────────────────────
	sort.SliceStable(fileOrder, func(i, j int) bool {
		return fileMap[fileOrder[i]].avg < fileMap[fileOrder[j]].avg
	})

	// ── Build output ──────────────────────────────────────────────────────────
	const sepBar = "--------  " + "-----------------------------------------------------------------------"
	const header = "=== Uncovered Functions by File (sorted: lowest first) ==="

	type row struct {
		plain string
		color string // ANSI colour for terminal; empty = no colour
	}

	var rows []row
	add := func(plain, color string) { rows = append(rows, row{plain, color}) }

	add("", "")
	add(header, colCyan+colBold)
	add(sepBar, "")
	if len(excludes) > 0 {
		add(fmt.Sprintf("  (excluding: %s)", strings.Join(excludes, ", ")), colReset)
	}

	skipped := 0
	for _, k := range fileOrder {
		fe := fileMap[k]
		if fe.avg >= 100.0 {
			skipped++
			continue
		}

		fileColor := colYellow
		if fe.avg == 0 {
			fileColor = colRed
		} else if fe.avg >= 50 {
			fileColor = colReset
		}

		add("", "")
		add(fmt.Sprintf("  %6.1f%%  %s", fe.avg, fe.path), fileColor)

		for _, f := range fe.fns {
			if f.pct >= 100.0 {
				continue
			}
			fnColor := colYellow
			if f.pct == 0 {
				fnColor = colRed
			}
			add(fmt.Sprintf("           :%-6d %-40s %6.1f%%", f.line, f.name, f.pct), fnColor)
		}
	}

	add("", "")
	add(sepBar, "")
	add(fmt.Sprintf("  TOTAL     %-10s   (%d file(s) at 100%%, omitted)", totalStr, skipped), colCyan)
	add("", "")

	// ── Print to terminal with ANSI colours ───────────────────────────────────
	for _, r := range rows {
		if r.color != "" {
			fmt.Printf("%s%s%s\n", r.color, r.plain, colReset)
		} else {
			fmt.Println(r.plain)
		}
	}

	// ── Write plain text (no ANSI) to file ───────────────────────────────────
	f, err := os.Create(outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create %s: %v\n", outFile, err)
		os.Exit(1)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, r := range rows {
		fmt.Fprintln(w, r.plain)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "write failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[OK] Report saved to %s\n", outFile)
}

func isExcluded(filePath string, excludes []string) bool {
	for _, e := range excludes {
		if strings.HasPrefix(filePath, e) {
			return true
		}
	}
	return false
}
