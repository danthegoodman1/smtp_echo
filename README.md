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

## DNS requirements

If you are using `mailtest.httpkv.com` as your mail subdomain, set:

- `A` `mailtest.httpkv.com` -> `<server_public_ip>`
- `MX` `mailtest.httpkv.com` -> `10 mailtest.httpkv.com`
- `PTR` `<server_public_ip>` -> `mailtest.httpkv.com` (set at your provider)
- `TXT` `mailtest.httpkv.com` -> `"v=spf1 ip4:<server_public_ip> -all"`
- `TXT` `_dmarc.mailtest.httpkv.com` -> `"v=DMARC1; p=none; rua=mailto:dmarc@httpkv.com"`

Notes:

- if your recipient addresses are `user@mailtest.httpkv.com`, the MX must exist on `mailtest.httpkv.com`
- DMARC on `_dmarc.mailtest.httpkv.com` is enough for `From: *@mailtest.httpkv.com`
- `p=none` is a monitor-only DMARC policy and is a good starting point

Quick checks:

```bash
dig +short A mailtest.httpkv.com
dig +short MX mailtest.httpkv.com
dig +short -x <server_public_ip>
dig +short TXT mailtest.httpkv.com
dig +short TXT _dmarc.mailtest.httpkv.com
```

## Run

```bash
go run ./cmd/smtp-echo -config config.yaml
```

## Manual verification

1. Deploy on a host with inbound and outbound port `25` available.
2. Send an email from an external mailbox to this server.
3. Confirm an echoed reply arrives in the same conversation thread.
4. Confirm the server is delivering directly to MX hosts (no relay configured).
