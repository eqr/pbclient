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
	client, err := pbclient.NewClient("https://your-pocketbase-host")
	if err != nil {
		log.Fatal(err)
	}

	authed, err := client.AuthenticateSuperuser(pbclient.Credentials{
		Email:    "admin@example.com",
		Password: "super-secret",
	})
	if err != nil {
		log.Fatal(err)
	}

	kv := pbclient.NewTypedKVStore[map[string]any](authed, "kv_store", "myapp")
	ctx := context.Background()

	if err := kv.Set(ctx, "feature_alpha", map[string]any{"enabled": true}); err != nil {
		log.Fatal(err)
	}

	flag, err := kv.Get(ctx, "feature_alpha")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("feature_alpha: %+v\n", flag)

	keys, _ := kv.List(ctx, "feature_")
	fmt.Printf("keys: %v\n", keys)

	exists, _ := kv.Exists(ctx, "feature_alpha")
	fmt.Printf("exists? %v\n", exists)

	_ = kv.Delete(ctx, "feature_alpha")
}
