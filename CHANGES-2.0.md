# Agent 2.0

- Use only `/api/clients/v2/rpc` for reports, basic information, commands and ping results. Removed v1 negotiation and payloads.
- Share one HTTP implementation for all v2 requests; preserve WebSocket recovery and v2 HTTP long polling.
- Remote-control opt-out, terminal/file streams and MOTD cleanup remain active.

Validation: full Go tests and race checks for server, command setup and system monitoring. Deploy against a v2-capable panel before retiring its old endpoints.
