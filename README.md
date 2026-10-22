# stalwart-jev

Answers Stalwart's LLM spam classifier (`SpamLlm`) from TypeSafe Jev. It
serves `POST /v1/chat/completions` in the shape Stalwart calls, asks Jev one
Choice question over the message's subject and body, and replies
`<Category>,<High|Medium|Low>,p=<probability>`. Categories are Stalwart's
defaults: `Unsolicited`, `Commercial`, `Harmful`, `Legitimate`.

Stalwart builds its request as `<prompt>\n\nSubject: <subject>\n\n<body>`; the
prompt configured in `SpamLlm` must not itself contain `\n\nSubject: `.
`SpamLlm` is a Stalwart Enterprise feature: without a license Stalwart never
calls the classifier.

## Install

The chart is published as `oci://sarak.sh/mikluko/charts/stalwart-jev`, the
image as `sarak.sh/mikluko/stalwart-jev`, both versioned `X.Y.Z` from the
`vX.Y.Z` tag.

```shell
helm install stalwart-jev oci://sarak.sh/mikluko/charts/stalwart-jev \
  --namespace mail --set apiKey=<TypeSafe API key>
```

The chart's NetworkPolicy admits only pods labelled
`app.kubernetes.io/name: stalwart`; set `networkPolicy.allowFrom` to match your
Stalwart pods.

## Stalwart

Point an `AiModel` at the service and enable `SpamLlm` with it, as
`stalwart-cli apply` plan lines:

```json
{"@type":"upsert","object":"AiModel","matchOn":["name"],"value":{"jev":{"name":"jev","url":"http://stalwart-jev:8080/v1/chat/completions","model":"jev-latest","modelType":"Chat","timeout":30000}}}
{"@type":"update","object":"SpamLlm","value":{"@type":"Enable","modelId":"#jev","prompt":"Classify this email.","categories":{"Unsolicited":true,"Commercial":true,"Harmful":true,"Legitimate":true},"confidence":{"High":true,"Medium":true,"Low":true},"separator":",","responsePosCategory":0,"responsePosConfidence":1,"responsePosExplanation":2,"temperature":0.0}}
```

Stalwart tags each message `LLM_<CATEGORY>_<CONFIDENCE>`, for example
`LLM_HARMFUL_HIGH`. Its shipped rules give these tags no score, so the verdict
changes nothing until `SpamTag` entries weigh them:

```json
{"@type":"upsert","object":"SpamTag","matchOn":["tag"],"value":{"llm-harmful-high":{"@type":"Score","tag":"LLM_HARMFUL_HIGH","score":7.0},"llm-legitimate-high":{"@type":"Score","tag":"LLM_LEGITIMATE_HIGH","score":-3.0}}}
```

When the service fails or times out, Stalwart delivers the message untagged.

## Configuration

| Variable | Default | |
|---|---|---|
| `TYPESAFE_API_KEY` | required | |
| `TYPESAFE_MODEL` | `jev-latest` | |
| `TYPESAFE_URL` | `https://api.typesafe.ai/v1/systemone` | |
| `TYPESAFE_TIMEOUT` | `20s` | per request to TypeSafe |
| `MAX_BODY_BYTES` | `32768` | body sent to Jev is cut to this |
| `CONFIDENCE_HIGH` | `0.8` | lowest Choice confidence reported as High |
| `CONFIDENCE_MEDIUM` | `0.5` | lowest reported as Medium; below is Low |
| `LISTEN_ADDR` | `:8080` | |

Telemetry is OpenTelemetry over OTLP, set by the standard `OTEL_*`
variables. Metrics: `stalwart_jev.verdicts` by `category` and `confidence`,
`stalwart_jev.failures` by `reason`, `stalwart_jev.confidence`,
`stalwart_jev.input_tokens`. Logs are JSON on stdout with `trace_id`.
