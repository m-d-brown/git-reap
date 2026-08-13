package main

import "testing"

func TestParseArgs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		opts, err := parseArgs(nil)
		if err != nil {
			t.Fatal(err)
		}
		want := options{days: defaultDays, fetch: true}
		if opts != want {
			t.Errorf("parseArgs(nil) = %+v, want %+v", opts, want)
		}
	})

	t.Run("options", func(t *testing.T) {
		opts, err := parseArgs([]string{"-a", "-y", "-d", "30", "--no-fetch", "develop"})
		if err != nil {
			t.Fatal(err)
		}
		want := options{base: "develop", all: true, yes: true, days: 30}
		if opts != want {
			t.Errorf("parseArgs = %+v, want %+v", opts, want)
		}
	})

	t.Run("long names", func(t *testing.T) {
		opts, err := parseArgs([]string{"--dry-run", "--days", "7"})
		if err != nil {
			t.Fatal(err)
		}
		if !opts.dryRun || opts.days != 7 {
			t.Errorf("parseArgs = %+v", opts)
		}
	})

	t.Run("options may follow the base", func(t *testing.T) {
		opts, err := parseArgs([]string{"develop", "-n"})
		if err != nil {
			t.Fatal(err)
		}
		if opts.base != "develop" || !opts.dryRun {
			t.Errorf("parseArgs = %+v", opts)
		}
	})

	t.Run("a second base is an error", func(t *testing.T) {
		if _, err := parseArgs([]string{"develop", "main"}); err == nil {
			t.Error("parseArgs = nil error, want a complaint about the extra argument")
		}
	})

	t.Run("an unknown option is an error", func(t *testing.T) {
		if _, err := parseArgs([]string{"--nope"}); err == nil {
			t.Error("parseArgs = nil error, want a complaint about the unknown option")
		}
	})
}
