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
		return fmt.Errorf("js error: %s", res.ExceptionDetails.Text)
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
