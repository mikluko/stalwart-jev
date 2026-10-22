# stalwart-jev

Answers Stalwart's LLM spam classifier (`SpamLlm`) from TypeSafe Jev. It
serves `POST /v1/chat/completions` in the shape Stalwart calls, asks Jev one
Choice question over the message's subject and body, and replies
`<Category>,<High|Medium|Low>,p=<probability>`. Categories are Stalwart's
defaults: `Unsolicited`, `Commercial`, `Harmful`, `Legitimate`.

Stalwart builds its request as `<prompt>\n\nSubject: <subject>\n\n<body>`; the
prompt configured in `SpamLlm` must not itself contain `\n\nSubject: `.

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
