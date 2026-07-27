# Fixtures

Envelope shapes here are real responses from the ThousandEyes v7 API. A fixture
invented from the specification only proves the code agrees with a reading of
the spec; one taken from the server proves it agrees with the server — which is
how the HAL content type, the three error shapes and the empty 404 body were all
found.

| File | Envelope from | Items | Captured |
| --- | --- | --- | --- |
| `alerts_page1.json`, `alerts_page2.json` | `GET /v7/alerts` | synthetic | 2026-07-27 |

`GetAlerts` is one of the 37 operations that accept a `cursor` and therefore
paginate, which is why it is the subject of the pagination tests.

Two deliberate departures from the live response:

- **Items are synthetic.** The lab tenant has no alerts, so a captured body
  would be `{"alerts":[],"_links":{…}}` and would prove nothing about merging.
  Field names are taken from the generated `Alert` model so the fixture matches
  what the SDK decodes into.
- **`_links.next` is added by the test.** The lab returns this collection in one
  page; the tests add a next href to exercise the walk.

Re-capture the envelope with:

```bash
curl -s -H "Authorization: Bearer $TE_TOKEN" \
  "https://api.thousandeyes.com/v7/alerts?aid=$TE_AID&window=1d"
```

Strip `aid` from every `_links.href` and confirm the token does not appear
before committing.
