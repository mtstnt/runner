package util

import (
	"encoding/json"
	"fmt"
	"os"
)

func dbg(v any, die ...bool) {
	w, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(w))
	if len(die) >= 1 && die[0] {
		os.Exit(1)
	}
}
