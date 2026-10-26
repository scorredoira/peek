package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod/lib/proto"
)

func cmdJS(ctx *cmdContext, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peek js <code>")
	}

	_, page, err := connect(ctx)
	if err != nil {
		return err
	}

	code := strings.Join(args, " ")

	// Use Runtime.evaluate directly with AwaitPromise so that
	// code returning a Promise is automatically awaited.
	res, err := proto.RuntimeEvaluate{
		Expression:    code,
		AwaitPromise:  true,
		ReturnByValue: true,
	}.Call(page)
	if err != nil {
		return fmt.Errorf("js error: %w", err)
	}

	if res.ExceptionDetails != nil {
		return fmt.Errorf("js error: %s", jsException(res.ExceptionDetails))
	}

	if res.Result == nil {
		return nil
	}

	v := res.Result.Value.Val()
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case string:
		fmt.Println(val)
	default:
		b, err := json.MarshalIndent(val, "", "  ")
		if err != nil {
			fmt.Println(v)
		} else {
			fmt.Println(string(b))
		}
	}

	return nil
}

// jsException is what a script threw, as the console would say it: the error and where
// ("TypeError: Cannot read properties of null (reading 'innerText') at <anonymous>:1:41").
// Chrome's own text is only "Uncaught", which says nothing of which line failed or why.
func jsException(d *proto.RuntimeExceptionDetails) string {
	if d.Exception != nil && d.Exception.Description != "" {
		return d.Exception.Description
	}
	if d.Exception != nil && !d.Exception.Value.Nil() {
		return d.Text + " " + d.Exception.Value.String()
	}
	return fmt.Sprintf("%s at line %d, column %d", d.Text, d.LineNumber+1, d.ColumnNumber+1)
}
