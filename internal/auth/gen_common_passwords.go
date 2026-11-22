//go:build ignore

// gen_common_passwords builds common-passwords.txt.gz from a downloaded password list (see
// common-passwords.md for the source and the exact command). Each line is normalized the way
// CheckPassword compares (NFKC, lower case); entries outside the policy's length range can never
// be chosen, so they are dropped. The output is sorted, de-duplicated and gzipped
// deterministically (no name or time in the header), so a rerun gives the same bytes.
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func main() {
	in := flag.String("in", "", "downloaded list, one password per line")
	out := flag.String("out", "common-passwords.txt.gz", "output file")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if !utf8.ValidString(line) || strings.ContainsRune(line, '\n') {
			continue
		}
		p := strings.ToLower(norm.NFKC.String(line))
		if n := utf8.RuneCountInString(p); n < 8 || n > 128 {
			continue
		}
		seen[p] = true
	}
	if err := sc.Err(); err != nil {
		return err
	}
	list := make([]string, 0, len(seen))
	for p := range seen {
		list = append(list, p)
	}
	slices.Sort(list)

	o, err := os.Create(out)
	if err != nil {
		return err
	}
	zw, err := gzip.NewWriterLevel(o, gzip.BestCompression)
	if err != nil {
		o.Close()
		return err
	}
	if _, err := zw.Write([]byte(strings.Join(list, "\n") + "\n")); err != nil {
		o.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		o.Close()
		return err
	}
	if err := o.Close(); err != nil {
		return err
	}
	fmt.Printf("%d passwords written to %s\n", len(list), out)
	return nil
}
