# emSign CA regression certificates

Public certificate chains from [the incident checker](https://emudhra.we-have-a-problem.com/), captured by the upstream project on 2026-10-20 and 2026-10-22, downloaded on 2026-10-28. The upstream project is licensed under Apache-2.0. These are test-only certificates; no private keys are included and these roots must never be installed into a runtime trust store.

The 12 logged chains and two no-CT variants exercise direct and AAA cross-signed routes. `roots.pem` contains the four public roots from `roots.json`, each verified against the upstream DER SHA256. Trusting these roots deliberately in the test isolates Go crypto/x509 distrust behavior from the host trust store and network availability.

- chains.zip SHA256: `7b949956955fc501a30f4d98d2f513d34c0bbbe92e29a968194c96e097ce2345`
- roots.json SHA256: `119f32cd81a6b5a4e3c4c2df5fe07daa90dc43cafa123a62f0394f1895d92380`
