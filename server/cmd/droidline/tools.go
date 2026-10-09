package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/KnifeLemon/Droidline/server/internal/hub"
	"github.com/KnifeLemon/Droidline/server/internal/inspect"
	"github.com/KnifeLemon/Droidline/server/internal/webdriver"
)

// cmdWebDriver runs the optional Appium-compatible bridge until Ctrl+C.
func cmdWebDriver(g globals, args []string) error {
	fs := flag.NewFlagSet("webdriver", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:4723", "address for WebDriver clients; keep it on 127.0.0.1 unless you trust the network")
	if err := fs.Parse(args); err != nil {
		return usageErr{err.Error()}
	}
	cl, err := dial(g)
	if err != nil {
		return err
	}
	defer cl.Close()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	fmt.Fprintf(os.Stderr, "WebDriver bridge on http://%s\nUse it as the Appium server URL. Stop with Ctrl+C.\n", ln.Addr())
	if !loopback {
		fmt.Fprintln(os.Stderr, "Warning: anyone who can reach this address can control your phones; the bridge has no token.")
	}
	return http.Serve(ln, webdriver.New(cl, hub.Version, loopback).Handler())
}

// cmdInspect serves the optional inspector page until Ctrl+C. Recording starts only
// when you press Start recording on the page.
func cmdInspect(g globals, args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8781", "address for the inspector page")
	if err := fs.Parse(args); err != nil {
		return usageErr{err.Error()}
	}
	cl, err := dial(g)
	if err != nil {
		return err
	}
	defer cl.Close()
	events, err := dial(g)
	if err != nil {
		return err
	}
	defer events.Close()
	if res, err := events.Call(map[string]any{"cmd": "subscribe", "events": []any{"action"}}); err != nil {
		return err
	} else if err := failure(res); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	in := inspect.New(cl, loopback)
	go in.Listen(events.Events)
	fmt.Fprintf(os.Stderr, "Inspector on http://%s\nOpen it in a browser. Stop with Ctrl+C.\n", ln.Addr())
	if !loopback {
		fmt.Fprintln(os.Stderr, "Warning: anyone who can reach this address can see and control your phones.")
	}
	return http.Serve(ln, in.Handler())
}
