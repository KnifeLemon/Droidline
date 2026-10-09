package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"rsc.io/qr"

	"github.com/KnifeLemon/Droidline/server/internal/store"
	"github.com/KnifeLemon/Droidline/spec"
)

var groupOrder = []struct{ key, title string }{
	{"screen", "Screen"}, {"coords", "Coordinates"}, {"object", "Elements"}, {"input", "Input"},
	{"check", "Checks"}, {"condition", "Conditions"}, {"device", "Device"},
	{"app", "Apps"}, {"system", "System"}, {"network", "Network"}, {"chrome", "Chrome"},
	{"notification", "Notifications"}, {"server", "Server"},
}

func usage(w io.Writer) {
	sp := spec.MustLoad()
	lang := store.SystemLang()
	fmt.Fprint(w, `Droidline drives Android phones without ADB.

Setup
  droidline serve                 run the PC server (keep it open)
  droidline pair                  show a QR code to pair a phone
  droidline pair <code>           approve the phone showing this 6-digit code
  droidline devices               list paired phones
  droidline mcp                   MCP server for AI agents (stdio)
  droidline doctor                check the setup

Optional
  droidline lease [device]        borrow a free phone so other scripts cannot use it
  droidline release <lease>       give a leased phone back (-d NAME frees a stuck phone)
  droidline webdriver             Appium-compatible WebDriver bridge on 127.0.0.1:4723
  droidline inspect               screen inspector and recorder on http://127.0.0.1:8781

Settings
  droidline token create|list|revoke     client tokens for remote access
  droidline relay set <url> <token>      reach phones on mobile data through your relay
  droidline proxy add|list|remove        saved upstream proxies (@name)
  droidline webhook secret               signing secret for notification webhooks

Global options
  --device, -d NAME   which phone (needed when more than one is online)
  --lease ID          send this lease with phone commands (from droidline lease)
  --json              print the raw response
  --addr HOST:PORT    server address (default 127.0.0.1:8780)
  --token TOKEN       client token
  --home DIR          settings folder (server side)

Phone commands (droidline help <command> for details)
`)
	for _, g := range groupOrder {
		var names []string
		for _, c := range sp.Commands {
			if c.Group == g.key && c.Scope != "server" {
				names = append(names, c.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		fmt.Fprintf(w, "  %-14s %s\n", g.title, strings.Join(names, " "))
	}
	fmt.Fprintf(w, "\nExamples\n  droidline launch com.android.chrome\n  droidline dump screen.json\n  droidline touch text \"%s\"\n  droidline touchById login\n  droidline screenshot a.png --scale 0.5\n  droidline which text=Login id=main_tab --timeout 15\n  droidline batch '[[\"home\"],[\"sleep\",500],[\"recents\"]]'\n",
		map[string]string{"ko": "로그인", "zh": "登录"}[lang]+map[bool]string{true: "Log in", false: ""}[lang == "en"])
}

func cmdHelp(args []string) error {
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	sp := spec.MustLoad()
	cmd, aliasBy := sp.Command(args[0])
	if cmd == nil {
		return usageErr{fmt.Sprintf("no command %q", args[0])}
	}
	lang := store.SystemLang()
	fmt.Printf("%s\n  %s\n", signature(cmd, aliasBy, args[0]), cmd.Summary.In(lang))
	if n := cmd.Notes.In(lang); n != "" {
		fmt.Printf("  %s\n", n)
	}
	fmt.Println()
	for _, p := range cmd.Params {
		if aliasBy != "" && p.Name == "by" || p.Type == "function" {
			continue
		}
		def := ""
		if p.Default != nil {
			def = fmt.Sprintf(" (default %v)", p.Default)
		}
		req := ""
		if p.Required {
			req = " required"
		}
		fmt.Printf("  %-14s %s%s%s\n      %s\n", p.Name, p.Type, req, def, p.Doc.In(lang))
	}
	if cmd.Name == "touch" || aliasBy != "" {
		fmt.Println("\n  by: " + strings.Join(sp.SelectorNames(), ", "))
	}
	if cmd.Example != "" {
		fmt.Printf("\n  SDK: d.%s\n", cmd.Example)
	}
	if len(cmd.Errors) > 0 {
		fmt.Printf("  errors: %s\n", strings.Join(cmd.Errors, ", "))
	}
	return nil
}

func signature(cmd *spec.Command, aliasBy, name string) string {
	var parts []string
	for _, p := range cmd.Params {
		if aliasBy != "" && p.Name == "by" || p.Type == "function" || (p.ClientOnly && p.Name != "path") {
			continue
		}
		if p.Required {
			parts = append(parts, "<"+p.Name+">")
		} else {
			parts = append(parts, "["+p.Name+"]")
		}
	}
	return strings.TrimSpace("droidline " + name + " " + strings.Join(parts, " "))
}

// printQR draws the code with half-block characters so it fits a normal terminal.
func printQR(w io.Writer, text string) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return err
	}
	const quiet = 2
	n := code.Size
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= n || y >= n {
			return false
		}
		return code.Black(x, y)
	}
	var b strings.Builder
	for y := -quiet; y < n+quiet; y += 2 {
		for x := -quiet; x < n+quiet; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune(' ')
			case top:
				b.WriteRune('▄')
			case bottom:
				b.WriteRune('▀')
			default:
				b.WriteRune('█')
			}
		}
		b.WriteRune('\n')
	}
	_, err = io.WriteString(w, b.String())
	return err
}
