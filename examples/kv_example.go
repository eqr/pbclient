//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/eqr/pbclient"
)

func main() {
	client, err := pbclient.NewClient(
		"https://your-pocketbase-host",
		"admin@example.com",
		"super-secret",
	)
	if err != nil {
		log.Fatal(err)
	}

	kv := pbclient.NewKVStore(client, "kv_store")
	ctx := context.Background()

	if err := kv.Set(ctx, "feature_alpha", map[string]any{"enabled": true}); err != nil {
		log.Fatal(err)
	}

	var flag map[string]any
	if err := kv.Get(ctx, "feature_alpha", &flag); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("feature_alpha: %+v\n", flag)

	keys, _ := kv.List(ctx, "feature_")
	fmt.Printf("keys: %v\n", keys)

	exists, _ := kv.Exists(ctx, "feature_alpha")
	fmt.Printf("exists? %v\n", exists)

	_ = kv.Delete(ctx, "feature_alpha")
}
