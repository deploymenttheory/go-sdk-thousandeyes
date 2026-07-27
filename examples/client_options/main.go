// Configure the client explicitly rather than from the environment.
//
//	TE_TOKEN=... go run ./examples/client_options
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	te "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/config"
)

func main() {
	client, err := te.NewClient(
		&config.AuthConfig{
			BearerToken: os.Getenv("TE_TOKEN"),
			// Optional. When empty the token's default account group is used.
			AccountGroupID: os.Getenv("TE_AID"),
		},
		te.WithTimeout(30*time.Second),
		te.WithRetryCount(3),
		te.WithUserAgent("example/1.0"),
	)
	if err != nil {
		log.Fatalf("creating client: %v", err)
	}

	accountGroups, _, err := client.API.AccountGroups.GetAccountGroups(context.Background())
	if err != nil {
		log.Fatalf("listing account groups: %v", err)
	}

	for _, group := range accountGroups.AccountGroups {
		var name string
		if group.AccountGroupName != nil {
			name = *group.AccountGroupName
		}
		// AID is a named string type rather than a pointer, because the
		// specification marks it required.
		fmt.Printf("%-40s %s\n", name, group.AID)
	}
}
