// ibtdump prints information from iRacing .ibt telemetry files.
//
// Usage:
//
//	ibtdump <file.ibt>                              # header info + variable list
//	ibtdump -csv <file.ibt>                         # all samples as CSV
//	ibtdump -csv -vars Speed,Throttle,Brake <file>  # selected variables only
//	ibtdump -csv -n 100 <file.ibt>                  # first 100 samples
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rickymw/SimAppLauncher/internal/ibt"
)

func main() {
	flagCSV := flag.Bool("csv", false, "dump samples as CSV")
	flagVars := flag.String("vars", "", "comma-separated variable names to include in CSV (default: all)")
	flagN := flag.Int("n", 0, "limit CSV output to first N samples (0 = all)")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: ibtdump [flags] <file.ibt>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		flag.Usage()
		os.Exit(1)
	}
	path := args[0]

	f, err := ibt.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ibtdump: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if *flagCSV {
		if err := dumpCSV(f, *flagVars, *flagN); err != nil {
			fmt.Fprintf(os.Stderr, "ibtdump: %v\n", err)
			os.Exit(1)
		}
	} else {
		dumpInfo(f)
	}
}

// dumpInfo prints a human-readable summary of the file headers and variable list.
func dumpInfo(f *ibt.File) {
	h := f.Header()
	dh := f.DiskHeader()

	fmt.Println("=== Header ===")
	fmt.Printf("  Version:    %d\n", h.Ver)
	fmt.Printf("  TickRate:   %d Hz\n", h.TickRate)
	fmt.Printf("  NumVars:    %d\n", h.NumVars)
	fmt.Printf("  BufLen:     %d bytes\n", h.BufLen)
	fmt.Printf("  DataOffset: %d\n", h.DataOffset)

	fmt.Println()
	fmt.Println("=== Disk Header ===")
	fmt.Printf("  SessionStartDate: %s\n", dh.SessionStartDate.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("  SessionStartTime: %.1f s\n", dh.SessionStartTime)
	fmt.Printf("  SessionEndTime:   %.1f s\n", dh.SessionEndTime)
	fmt.Printf("  SessionLapCount:  %d\n", dh.SessionLapCount)
	fmt.Printf("  NumSamples:       %d\n", f.NumSamples())

	fmt.Println()
	fmt.Println("=== Variables ===")
	fmt.Printf("  %-32s %-10s %6s  %s\n", "Name", "Type", "Count", "Unit")
	fmt.Printf("  %-32s %-10s %6s  %s\n", strings.Repeat("-", 32), strings.Repeat("-", 10), "------", "----")
	for _, vd := range f.Vars() {
		fmt.Printf("  %-32s %-10s %6d  %s\n", vd.Name, vd.Type, vd.Count, vd.Unit)
	}
}

// dumpCSV writes all (or selected) variables as CSV rows to stdout.
func dumpCSV(f *ibt.File, varsFlag string, limitN int) error {
	// Determine which vars to include.
	var selected []ibt.VarDef
	if varsFlag == "" {
		selected = f.Vars()
	} else {
		names := strings.Split(varsFlag, ",")
		for _, name := range names {
			name = strings.TrimSpace(name)
			vd, ok := f.VarDef(name)
			if !ok {
				return fmt.Errorf("variable %q not found in file", name)
			}
			selected = append(selected, vd)
		}
	}

	// Build CSV header row — array vars expand to Name[0], Name[1], ...
	var header []string
	for _, vd := range selected {
		if vd.Count == 1 {
			header = append(header, vd.Name)
		} else {
			for j := 0; j < vd.Count; j++ {
				header = append(header, fmt.Sprintf("%s[%d]", vd.Name, j))
			}
		}
	}

	w := csv.NewWriter(os.Stdout)
	if err := w.Write(header); err != nil {
		return fmt.Errorf("writing CSV header: %w", err)
	}

	total := f.NumSamples()
	if limitN > 0 && limitN < total {
		total = limitN
	}

	for i := 0; i < total; i++ {
		s, err := f.Sample(i)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ibtdump: skipping sample %d: %v\n", i, err)
			break
		}

		var row []string
		for _, vd := range selected {
			row = append(row, sampleValues(s, vd)...)
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("writing CSV row %d: %w", i, err)
		}
	}

	w.Flush()
	return w.Error()
}

// sampleValues returns the string-formatted values for one variable in one sample.
// Array variables return multiple strings; scalars return one.
func sampleValues(s ibt.Sample, vd ibt.VarDef) []string {
	switch vd.Type {
	case ibt.VarTypeFloat:
		vals, _ := s.Float32s(vd.Name)
		if vals == nil {
			return zeroes(vd.Count)
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = strconv.FormatFloat(float64(v), 'f', -1, 32)
		}
		return out

	case ibt.VarTypeDouble:
		vals, _ := s.Float64s(vd.Name)
		if vals == nil {
			return zeroes(vd.Count)
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = strconv.FormatFloat(v, 'f', -1, 64)
		}
		return out

	case ibt.VarTypeInt:
		vals, _ := s.Ints(vd.Name)
		if vals == nil {
			return zeroes(vd.Count)
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = strconv.FormatInt(int64(v), 10)
		}
		return out

	case ibt.VarTypeBitField:
		v, _ := s.BitField(vd.Name)
		return []string{strconv.FormatUint(uint64(v), 10)}

	case ibt.VarTypeBool:
		v, ok := s.Bool(vd.Name)
		if !ok {
			return []string{"false"}
		}
		return []string{strconv.FormatBool(v)}

	default:
		return zeroes(vd.Count)
	}
}

// zeroes returns a slice of n "0" strings.
func zeroes(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "0"
	}
	return out
}
