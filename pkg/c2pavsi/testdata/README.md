# C2PA VSI test fixtures

For each track of `testpic_2s` (`V300` video, `A48` audio):

- **`raw/`**: the init segment and three consecutive media segments exactly as livesim2 served them.
- **`c2pars/`**: the same files signed by c2pa-rs's `LiveVideoVsiSigner`, which is the reference implementation:
  - `init.mp4` carries a C2PA manifest with a `c2pa.session-keys` assertion.
  - Each media segment carries a VSI `emsg` box.
  - `vsi.json` records the session key seed, `kid` and public key, and the init manifest's ID.

The c2pa-rs signer comes from the experimental live video support in [contentauth/c2pa-rs#2862](https://github.com/contentauth/c2pa-rs/issues/2862), at commit `8f16c0f78673461ba41c184e71d29a6492be571c` of [mrappard/c2pa-rs-text-support](https://github.com/mrappard/c2pa-rs-text-support). The init manifest is signed with c2pa-rs's public ES256 **test** certificate, and the session key seed is the fixed test value `000102…1f`.

The fixtures were generated with the `vsi-golden` tool from the LiveSim C2PA demo:

```
vsi-golden make --certs <es256 test certs> --seed 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f \
  --kid testpic-V300 --init raw/init.mp4 --out c2pars raw/*.m4s
```

The tests check both directions:

- **c2pa-rs → Go:** this package verifies the c2pa-rs-signed segments. That confirms it computes the same `bmffHash` and reads the same COSE_Sign1 encoding.
- **Go → c2pa-rs:** this package signs the raw segments with the same session key. To check those with c2pa-rs's validator, write them out and run `vsi-golden check`:
  ```
  C2PAVSI_OUT=/tmp/vsi go test ./pkg/c2pavsi -run Interop
  vsi-golden check --init /tmp/vsi/V300/init.mp4 /tmp/vsi/V300/*.m4s
  ```
