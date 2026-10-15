# Pre-signed C2PA init segments for `c2pa_vsi`

Each `<representation id>/` directory enables the `c2pa_vsi` option for that representation:

- **`init.mp4`** is the init segment livesim2 serves for the representation, signed with a C2PA manifest.
  - The manifest's `c2pa.session-keys` assertion announces an Ed25519 session key.
  - livesim2 checks at startup that this file, with the C2PA `uuid` box removed, is exactly the init segment it serves. If the two don't match, VSI isn't enabled.
- **`vsi.json`** holds the manifest's identifier (`manifestId`) and the session key's `kid` and 32-byte `seed` (hex). livesim2 uses them to sign each media segment's VSI `emsg` box.

These are **test credentials**:
- The manifest is signed with c2pa-rs's public ES256 sample certificate, so it validates as untrusted.
- The session key seeds are fixed test values.
- The keys are valid for ten years from signing. Segments with a presentation time before that point fail validation.

To regenerate after a change to how livesim2 builds init segments:

1. Fetch the init segments livesim2 serves.
2. Sign them with c2pa-rs, for example with `vsi-golden` from the LiveSim C2PA demo:

```
curl -o V300-init.mp4 http://localhost:8888/livesim2/testpic_2s/V300/init.mp4
vsi-golden make --certs <es256 test certs> --seed <64 hex digits> --kid testpic_2s-V300 \
  --init V300-init.mp4 --out V300
```
