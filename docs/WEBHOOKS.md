# Webhooks

[← Back to README](../README.md) · [Documentation index](DOCUMENTATION.md)

A **Webhook** alert channel sends a JSON `POST` for every event.

## Payload

```json
{
  "event": "down",
  "title": "🔴 API is DOWN",
  "message": "…",
  "reason": "Connection refused",
  "time": "2026-10-27T09:30:00Z",
  "url": "https://hook.example.com/monitors/3",
  "monitor": { "id": 3, "name": "API", "type": "http", "target": "https://api.example.com" }
}
```

## Events

| `event` | Sent when |
|---|---|
| `down`, `up` | A monitor goes down or recovers |
| `cert` | A certificate expires in 14, 7, 3 or 1 days |
| `offline`, `online` | A server stops or resumes reporting |
| `threshold`, `recovered` | CPU, memory or disk crosses its limit, or clears it |
| `container`, `container_ok`, `restart` | A container has a problem, recovers, or restarts after a crash |
| `service`, `service_ok` | A watched systemd unit stops or recovers |
| `forecast` | A disk is projected to fill within 7 days |
| `test` | You pressed **Send test** |

## Extra fields

- Server events carry a `server` object instead of `monitor`.
- Container and service events add `subject`: the container or unit name.
- Threshold events add `metric`: `{name, value, peak, limit, minutes}`.
- Recovery events add `downtimeSeconds`.
- Certificate events add `certDaysLeft`.

## Signature

When the channel has a secret, each request carries this header:

```
X-Hook-Signature: sha256=<hex>
```

`<hex>` is an HMAC-SHA256 of the raw request body, keyed with the secret. Compute the same value on your side and compare the two in constant time.

Deliveries don't follow redirects; a 3xx response counts as a failed delivery.
