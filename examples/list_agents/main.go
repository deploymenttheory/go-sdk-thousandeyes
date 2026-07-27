// List the Cloud and Enterprise agents visible to the token.
//
//	TE_TOKEN=... go run ./examples/list_agents
package main

import (
	"context"
	"fmt"
	"log"

	te "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes"
)

func main() {
	client, err := te.NewClientFromEnv()
	if err != nil {
		log.Fatalf("creating client: %v", err)
	}

	agents, _, err := client.API.CloudAndEnterpriseAgents.GetAgents(context.Background())
	if err != nil {
		log.Fatalf("listing agents: %v", err)
	}

	fmt.Printf("%d agents\n", len(agents.Agents))

	// The organization quota is reported on every response, so it is known
	// after the first call without asking for it.
	if quota := client.Transport.RateLimit(); quota.Known {
		fmt.Printf("rate limit: %d/%d remaining, resets %s\n",
			quota.Remaining, quota.Limit, quota.Reset.Format("15:04:05"))
	}
}
