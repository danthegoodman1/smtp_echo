# smtp_echo

Minimal SMTP echo daemon for infrastructure validation. It accepts inbound email and replies in-thread with the same message body, delivering directly to recipient MX hosts on port `25` (no external relay).

## What it tests

- inbound SMTP reachability to your host
- outbound direct SMTP delivery from your host
- DNS/MX/rDNS posture in realistic mail flow
- thread linkage via `In-Reply-To` and `References`

## Configuration

Copy `config.example.yaml` to `config.yaml` and edit values:

- `listen_addr`: inbound bind address (usually `:25`)
- `hostname`: your SMTP hostname used for server domain/EHLO
- `read_timeout`, `write_timeout`, `max_message_bytes`
- `reply.from_address`: visible `From:` in echoed reply
- `reply.mail_from`: SMTP envelope sender for outbound `MAIL FROM`
- `reply.from_name`: optional display name

## Run

```bash
go run ./cmd/smtp-echo -config config.yaml
```

## Manual verification

1. Deploy on a host with inbound and outbound port `25` available.
2. Send an email from an external mailbox to this server.
3. Confirm an echoed reply arrives in the same conversation thread.
4. Confirm the server is delivering directly to MX hosts (no relay configured).
