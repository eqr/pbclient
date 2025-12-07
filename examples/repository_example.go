//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/eqr/pbclient"
)

type Todo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

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

	repo := pbclient.NewRepository[Todo](authed, "todos")
	ctx := context.Background()

	created, err := repo.Create(ctx, Todo{Title: "write docs", Done: false})
	if err != nil {
		log.Fatal(err)
	}

	list, err := repo.List(ctx, pbclient.ListOptions{
		Page:    1,
		PerPage: 10,
		Filter:  pbclient.Eq("done", "false"),
		Sort:    "-created",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("items: %+v\n", list.Items)

	_, _ = repo.Update(ctx, created.ID, Todo{Title: "write docs", Done: true})
	_ = repo.Delete(ctx, created.ID)
}
