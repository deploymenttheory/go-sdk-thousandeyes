// Show how paginated operations behave.
//
// Operations that accept a cursor fetch every page by default, so a collection
// comes back whole rather than silently truncated at the first page.
//
//	TE_TOKEN=... go run ./examples/pagination
package main

import (
	"context"
	"fmt"
	"log"

	te "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/client"
)

func main() {
	c, err := te.NewClientFromEnv()
	if err != nil {
		log.Fatalf("creating client: %v", err)
	}
	ctx := context.Background()

	// Every page, merged.
	all, _, err := c.API.Alerts.GetAlerts(ctx, client.WithWindow("7d"))
	if err != nil {
		log.Fatalf("listing alerts: %v", err)
	}
	fmt.Printf("all pages:  %d alerts\n", len(all.Alerts))

	// One page. WithMax sets the page size; WithMaxPages bounds the walk.
	first, _, err := c.API.Alerts.GetAlerts(ctx,
		client.WithWindow("7d"),
		client.WithMax(10),
		client.WithMaxPages(1),
	)
	if err != nil {
		log.Fatalf("listing first page: %v", err)
	}
	fmt.Printf("first page: %d alerts\n", len(first.Alerts))
}
