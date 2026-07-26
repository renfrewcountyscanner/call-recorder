# Notifications

Migration 005 adds destinations, rules, and durable delivery history. Destinations support SMTP metadata, generic JSON webhooks, Discord, and Telegram; secret values are referenced by name and are never stored in PostgreSQL. Rules default disabled and deliveries are unique per rule/call. Run the worker with `call-recorder-admin notifications run`; retry with `notifications retry --delivery ID` and inspect `notifications history`.

Only HTTPS/HTTP endpoints should be used in production, with private-network/SSRF policy enforced at the deployment boundary. Automated tests use isolated fake HTTP endpoints and never send real messages.
