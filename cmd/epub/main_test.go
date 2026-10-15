package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pirmd/epub"
)

func TestReadMetadataUpdate(t *testing.T) {
	update, err := readMetadataUpdate(strings.NewReader(`{"Title":["New"],"Subject":[],"Series":"","Creator":null}`))
	if err != nil {
		t.Fatal(err)
	}

	if len(update.Title) != 1 || update.Title[0].Value != "New" ||
		update.Subject == nil || len(update.Subject) != 0 ||
		update.Series.Value != "" || update.Creator != nil {
		t.Fatalf("incorrect partial update: %+v", update)
	}
	for _, input := range []string{
		`null`, `[]`, ``, `{`, `{"Unknown":"value"}`, `{"Title":"not a list"}`,
		`{} {}`, `{} trailing`, `{"Series":[]}`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := readMetadataUpdate(strings.NewReader(input)); err == nil {
				t.Fatal("expected invalid input error")
			}
		})
	}
}
func TestReadAuthorRefinements(t *testing.T) {
	update, err := readMetadataUpdate(strings.NewReader(`{"Creator":[{"FullName":"Author","Refinements":[{"Property":"cdr:wd_id","Value":"Q42"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(update.Creator) != 1 || len(update.Creator[0].Refinements) != 1 ||
		update.Creator[0].Refinements[0].Property != "cdr:wd_id" ||
		update.Creator[0].Refinements[0].Value != "Q42" {
		t.Fatalf("custom author properties not decoded: %+v", update.Creator)
	}
}

func TestCLIProcess(t *testing.T) {
	if os.Getenv("EPUB_CLI_TEST_PROCESS") != "1" {
		return
	}
	os.Args = append([]string{"epub"}, os.Args[3:]...)
	flag.CommandLine = flag.NewFlagSet("epub", flag.ExitOnError)
	main()
}

func TestCLIWrite(t *testing.T) {
	fixtures, err := filepath.Glob("../../testdata/*.epub")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("cannot find EPUB fixtures: %v", err)
	}
	source, err := os.ReadFile(fixtures[0])
	if err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "EPUB_CLI_TEST_PROCESS=1")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, mode := range []string{"file", "stdin", "refined", "refined epub2", "invalid", "multiple", "missing", "clear", "invalid clear", "clear without write"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "book.epub")
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			update := `{"Title":["CLI title"],"Subject":[]}`
			var out string
			var err error
			switch mode {
			case "file":
				jsonPath := filepath.Join(dir, "metadata.json")
				if err := os.WriteFile(jsonPath, []byte(update), 0600); err != nil {
					t.Fatal(err)
				}
				out, err = run("", "--write", jsonPath, "--field", "title", path)
			case "stdin":
				out, err = run(update, "--write", "-", "--field", "title", path)
			case "refined", "refined epub2":
				fixture := "../../testdata/Row-Your-Boat-3.0.epub"
				if mode == "refined epub2" {
					fixture = "../../testdata/Row-Your-Boat-2.0.epub"
				}
				data, readErr := os.ReadFile(fixture)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				out, err = run(`{"Title":[{"Value":"CLI title","Refinements":[{"Property":"alternate-script","Value":"Other script"}]}],"Subject":[]}`,
					"--write", "-", "--field", "title", path)
				info, readErr := epub.GetMetadataFromFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(info.Title) != 1 || len(info.Title[0].Refinements) != 1 ||
					info.Title[0].Refinements[0].Property != "alternate-script" {
					t.Fatalf("CLI lost field refinements: %+v", info.Title)
				}
			case "clear":
				if err := epub.WriteMetadataToFile(path, &epub.Information{Series: epub.MetadataValue{Value: "Series"}, SeriesIndex: epub.MetadataValue{Value: "3"}}); err != nil {
					t.Fatal(err)
				}
				out, err = run(update, "--write", "-", "--clear", "Series", "--field", "title", path)
				info, readErr := epub.GetMetadataFromFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if info.Series.Value != "" || info.SeriesIndex.Value != "" {
					t.Fatalf("CLI did not clear series: %+v", info)
				}
			case "invalid clear", "clear without write":
				args := []string{"--clear", "Series", path}
				if mode == "invalid clear" {
					args = []string{"--write", "-", "--clear", "Unknown", path}
				}
				out, err = run(update, args...)
				if err == nil || !strings.Contains(out, "Error:") {
					t.Fatalf("invalid clear options accepted: %v\n%s", err, out)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil || string(after) != string(source) {
					t.Fatalf("invalid clear options modified original: %v", readErr)
				}
				return
			case "invalid":
				out, err = run(`{"Unknown":[]}`, "--write", "-", path)
				if err == nil || !strings.Contains(out, "unknown field") {
					t.Fatalf("invalid JSON was accepted: %v\n%s", err, out)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil || string(after) != string(source) {
					t.Fatalf("invalid input changed EPUB: %v", readErr)
				}
				return
			case "multiple":
				second := filepath.Join(dir, "second.epub")
				if err := os.WriteFile(second, source, 0600); err != nil {
					t.Fatal(err)
				}
				out, err = run(update, "--write", "-", "--field", "title", path, second)
				info, readErr := epub.GetMetadataFromFile(second)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(info.Title) != 1 || info.Title[0].Value != "CLI title" {
					t.Fatalf("second file was not updated: %+v", info)
				}
			case "missing":
				out, err = run(update, "--write", "-", "--field", "title", filepath.Join(dir, "missing.epub"), path)
				if err == nil || !strings.Contains(out, "Error updating epub") {
					t.Fatalf("missing file error not reported: %v\n%s", err, out)
				}
				err = nil
			}
			if err != nil || !strings.Contains(out, "[CLI title]") {
				t.Fatalf("CLI update failed: %v\n%s", err, out)
			}
			info, err := epub.GetMetadataFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(info.Title) != 1 || info.Title[0].Value != "CLI title" || len(info.Subject) != 0 {
				t.Fatalf("CLI update was not persisted: %+v", info)
			}
		})
	}
}
