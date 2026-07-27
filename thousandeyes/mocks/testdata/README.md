# Captured fixtures

Real responses from the ThousandEyes v7 API, captured with curl. A fixture
invented from the specification proves only that the code agrees with a reading
of the spec; one captured from the server proves it agrees with the server.

| File | Source | Captured |
| --- | --- | --- |
| `agents_list_200.json` | `GET /v7/agents` | 2026-07-27 |
| `error_400_problem.json` | `POST /v7/tests/bgp` with an invalid body | 2026-07-27 |

`agents_list_200.json` is trimmed to three agents. Both have the account id
stripped from `_links` hrefs and `instance`.

Re-capture with:

```bash
curl -s -H "Authorization: Bearer $TE_TOKEN" \
  "https://api.thousandeyes.com/v7/agents?aid=$TE_AID"

curl -s -X POST -H "Authorization: Bearer $TE_TOKEN" -H "Content-Type: application/json" \
  -d '{"bogusField":true}' "https://api.thousandeyes.com/v7/tests/bgp?aid=$TE_AID"
```

Strip the account id and confirm the token does not appear before committing.
