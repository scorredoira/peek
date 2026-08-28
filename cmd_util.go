package main

import (
	"fmt"
	"net"
	"strconv"

	"github.com/go-rod/rod/lib/proto"
)

func cmdTexts(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek texts --css <selector> [--limit N]")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	var selector string
	limit := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--css":
			if i+1 < len(args) {
				selector = args[i+1]
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err == nil {
					limit = n
				}
				i++
			}
		default:
			if selector == "" {
				selector = args[i]
			}
		}
	}

	if selector == "" {
		return fmt.Errorf("usage: peek texts --css <selector> [--limit N]")
	}

	jsCode := fmt.Sprintf(`(function(){
		var els = document.querySelectorAll(%q);
		var result = [];
		for (var i = 0; i < els.length && result.length < %d; i++) {
			var t = els[i].textContent.trim();
			if (t && t.length < 80) {
				result.push(t);
			}
		}
		return JSON.stringify({total: els.length, texts: result});
	})()`, selector, limit)

	res, err := proto.RuntimeEvaluate{Expression: jsCode}.Call(page)
	if err != nil {
		return fmt.Errorf("failed: %w", err)
	}

	fmt.Println(res.Result.Value.Str())
	return nil
}

func cmdFreeport() error {
	port, err := freePort()
	if err != nil {
		return err
	}
	fmt.Println(port)
	return nil
}

// execFreeport is the test-script version of freeport.
// Syntax: freeport PORT   → stores port in ${PORT}
// Syntax: freeport        → stores port in ${result}
func execFreeport(args []string, vars map[string]string) error {
	port, err := freePort()
	if err != nil {
		return err
	}

	varName := "result"
	if len(args) > 0 {
		varName = args[0]
	}

	vars[varName] = strconv.Itoa(port)
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}
