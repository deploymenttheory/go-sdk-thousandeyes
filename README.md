# Go SDK for the ThousandEyes API

A Go client for the [ThousandEyes API v7](https://developer.cisco.com/docs/thousandeyes/), generated
from Cisco's published OpenAPI specification.

**97 service packages · 315 operations · 641 enumerations · 18 discriminated unions**

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	te "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes"
)

func main() {
	// Reads TE_TOKEN, TE_AID and TE_API_ENDPOINT.
	client, err := te.NewClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	agents, _, err := client.API.CloudAndEnterpriseAgents.GetAgents(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%d agents\n", len(agents.Agents))
}
```

## Authentication

ThousandEyes has **no service-account or client-credentials identity type**. The v7 API accepts
exactly one credential: an OAuth2 bearer token belonging to a *user*, created by hand from
**Account Settings → Users and Roles → Profile → User API Tokens**. There is no token exchange,
refresh or revocation flow, and no API for minting a token on another user's behalf.

For automation, the closest equivalent is a dedicated user account on a mailbox the team controls,
holding a role scoped to what the automation needs.

```go
client, err := te.NewClient(&config.AuthConfig{
	BearerToken:    os.Getenv("TE_TOKEN"),
	AccountGroupID: "281474976742769", // optional; sent as aid on every request
})
```

| Variable | Purpose |
| --- | --- |
| `TE_TOKEN` | Bearer token. Required. |
| `TE_AID` | Account group. Optional; the token's default is used when unset. |
| `TE_API_ENDPOINT` | Base URL. Defaults to `https://api.thousandeyes.com/v7`. |

These are the same variables the ThousandEyes Terraform provider reads, so one exported environment
serves both.

## Client options

```go
client, err := te.NewClientFromEnv(
	te.WithTimeout(30*time.Second),
	te.WithRetryCount(3),
	te.WithLogger(logger),
)
```

Transport behaviour worth knowing:

- **Rate limiting is header-driven.** The API reports the organization quota on every response
  (`x-organization-rate-limit-{limit,remaining,reset}`), and the client paces itself against that
  budget rather than inferring pressure from latency. `client.Transport.RateLimit()` returns the last
  snapshot.
- **Retries** cover idempotent requests with exponential backoff.
- **Concurrency** is limited when `WithMaxConcurrentRequests` is set.

## Pagination

Read operations that accept a cursor **fetch every page** and return the whole collection. 37 of the
315 operations work this way; the rest return a single response.

```go
alerts, _, err := client.API.Alerts.GetAlerts(ctx) // all pages, merged
```

This is deliberate. Returning only the first page is the kind of bug that never announces itself —
the call succeeds, the data looks right, and it is quietly incomplete.

To bound the walk:

```go
client.API.Alerts.GetAlerts(ctx, tclient.WithMaxPages(1)) // one page
client.API.Alerts.GetAlerts(ctx, tclient.WithMax(50))     // 50 items per page
```

`WithMax` sets the page *size*; `WithMaxPages` limits how many pages are fetched. Note that some
collection endpoints — `/agents` among them — ignore paging parameters entirely and return
everything in one response.

### Known limitation: POST `/filter` endpoints

Ten operations page differently: they take their filter criteria as a POST body
— too complex for a query string — and page through a `cursor` query parameter,
the pattern used by Elasticsearch `_search` and similar search APIs.

The cursor is definitely real: `POST /v7/endpoint/agents/filter?cursor=abc`
returns `400 "Failed to deserialize"`, while an unknown parameter such as
`?bogusParam=abc` is silently ignored and returns 200. The server parses it as a
typed value.

**Multi-page behaviour on these endpoints is unverified.** Every tenant available
for testing returns a single empty page with `_links` as `{}` — not even a
`self` link — so a `next` href has never been observed from a filter endpoint.
That it appears in the usual place is taken from the specification rather than
from the server. The walk uses the same code path as the GET collections, so a
next link where expected will be followed; treat it as untested until proven
against a tenant with enough data to force a second page.

These endpoints also answer with `application/json` rather than
`application/hal+json`, and carry a total alongside the collection.

## Enumerations are open

Every enumeration in the specification becomes a named type with typed constants:

```go
if test.Type == tests.TestTypeHTTPServer { ... }
```

They are **open**: a value the specification does not list still decodes and compares as its
underlying string. The API adds values without a major version change, and a closed type would turn a
routine upstream addition into a decode failure.

```go
value.IsKnown()          // is this one of the documented values?
value.String()           // the value as the API spells it
tests.TestTypeValues()   // every documented value
```

## Discriminated unions

Where the API returns one of several shapes, the generated type carries the discriminator plus one
pointer per variant. Build one with a constructor so the tag and the variant cannot disagree:

```go
selector := endpoint.NewEndpointAgentSelectorConfigFromSpecificAgents(
	endpoint.EndpointSpecificAgentsSelectorConfig{Agents: []string{"1", "2"}},
)

if cfg, ok := response.GetSpecificAgents(); ok {
	fmt.Println(cfg.Agents)
}
```

An unrecognised discriminator is not an error — the payload is retained on `Raw`, so a variant added
upstream is still reachable.

## Errors

```go
if client.IsNotFound(err) { ... }
if client.IsRateLimited(err) { ... }
```

The API uses three unrelated error shapes — RFC 7807 `problem+json` on validation failures,
`{"error","error_description"}` for an invalid token, `{"errorMessage"}` when credentials are absent
— plus an empty body on 404. All four are normalised into `*client.APIError`.

## Generated code

`thousandeyes/thousandeyes_api/` and `thousandeyes/thousandeyes.go` are generated. Do not edit them;
change the generator or its templates and regenerate:

```bash
go run ./scripts/openapi/GenerateServices
```

The specification itself is version-controlled under `openapi-specs/`, fetched weekly by
`.github/workflows/get-openapi-specs.yml`, which opens a PR carrying both the new snapshot and the
regenerated code. `codegen-verify.yml` fails any PR whose generated output has drifted from the
specification it was generated from.

| Path | Contents |
| --- | --- |
| `thousandeyes/client` | transport, auth, retry, rate limiting, pagination |
| `thousandeyes/thousandeyes_api` | generated services, models, enums, unions |
| `thousandeyes/shared/templating` | `TemplatedValue[T]` for the Templates API |
| `internal/codegen`, `internal/templates` | the generator and its templates |
| `openapi-specs` | version-controlled specification snapshots |
| `examples` | runnable examples |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Unit tests are required for changed packages, and generated
code must be regenerated rather than hand-edited.
