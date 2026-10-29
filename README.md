# Draw Verification — published algorithm & in-browser verifier

This repository is the **published, independently-runnable verifier** for
Raffle Town's prize draws (Main Prize Draw, Instant Prizes, and Vault
Draws). It contains the complete selection algorithm and verification
logic — the same `drawproof` package the platform itself runs — so that anyone
can reproduce a draw's outcome from the published proof bundle **without
trusting the platform**.

- `drawproof/` — the algorithm and verifier. Pure functions, standard library
  only, no I/O. This is the entire trust anchor.
- `cmd/drawverify-wasm/` — compiles the verifier to WebAssembly, exposing a JS
  global `drawverify(bundleJSON)`.
- `docs/` — the verification web page plus the built WASM, suitable for static
  hosting (e.g. GitHub Pages). Every check is recomputed in your browser; the
  page also fetches the randomness beacon directly from the public
  [drand](https://drand.love) network, independent of the platform.

## Verify a draw

**In a browser.** Serve `docs/` from any static host and open it
(`python3 -m http.server -d docs` works), then paste a proof bundle — the
response of the platform's public proof feed
`GET <platform>/v2/raffles/rounds/<id>/verification` or
`/v2/raffles/draws/<id>/verification` (no authentication; proofs are published
once a promotion is finalised), or a previously saved bundle. The
platform-hosted copy at `<platform>/v2/verify/` additionally loads the feed
directly by round ID (it is same-origin with the API).

**As a Go library.**

```go
import "github.com/network-gaming/draw-verification-wasm/drawproof"

var b drawproof.Bundle // unmarshal a published proof bundle into this
res, err := drawproof.VerifyBundle(b)
// res.OK, res.Checks[i] = {Name, OK, Detail}
```

**Build the WASM yourself** (don't trust the committed binary):

```sh
make wasm        # GOOS=js GOARCH=wasm go build -trimpath -o docs/drawverify.wasm ./cmd/drawverify-wasm/
shasum -a 256 docs/drawverify.wasm
```

The build is `-trimpath` and dependency-free, so the same Go toolchain version
produces a byte-identical binary.

## How a verifiable draw works

1. **Commit, before the draw.** The platform writes a tamper-evident
   commitment to its immutable ledger: the SHA-256 hash of a secret local
   seed, a digest of the frozen input set (the eligible pool, or the complete
   instant-prize placement), and the number of a drand beacon pulse. For Main
   and Vault draws in future-pulse mode, that pulse has not yet been published
   — its value is unknowable at commit time.
2. **Reveal, after the draw.** The platform discloses the local seed, the
   beacon value, the final seed, and the outcome (winners, or the full
   allocation).
3. **Anyone re-derives the result.** The final seed is
   `SHA-256(localSeed ‖ beaconValue)` and the outcome is a pure function of
   `(input set, final seed)` — so the verifier reproduces it exactly, and the
   commit binds the inputs so none of them can have been swapped afterwards.

Instant Prizes cannot wait for an unpublished pulse (the placement must
exist before the first ticket is sold), so they mix the **latest published**
pulse; their binding guarantee is the pre-sales commitment, and the beacon
value bounds the earliest moment the placement could have been created.

## Algorithm specification

The construction is composed entirely of published primitives: SHA-256
(FIPS 180-4), HMAC-SHA256 (FIPS 198-1 / RFC 2104), the Fisher–Yates shuffle
(Knuth, TAOCP Vol. 2), and rejection sampling. The Go source in `drawproof/`
is normative; the composition is small enough to re-implement in any language:

**DRBG.** `key = SHA-256(seed)`. Keystream block *i* (for *i* = 0, 1, 2, …) is
`HMAC-SHA256(key, BE64(i))` where `BE64` is the 8-byte big-endian encoding.
Each 32-byte block yields four 64-bit values, consumed front-to-back as
big-endian unsigned integers.

**Unbiased index `intn(n)`.** Let `limit = ⌊2⁶⁴ / n⌋ · n`. Draw 64-bit values
from the DRBG, rejecting any `v ≥ limit`; return `v mod n`. This removes
modulo bias exactly.

**Winner selection** (`SelectWinners(pool, count, seed)`). The pool — a list
of entry-identifier strings — is first sorted bytewise (lexicographic) into
canonical order. Then a partial Fisher–Yates: for `i = 0 … count−1`, draw
`j = i + intn(n−i)`, swap elements `i` and `j`; element `i` is the *i*-th
winner. Winners are an unbiased sample without replacement, in selection
order.

**Instant placement** (`AllocateInstantPrizesV2(totalTickets, units, rules,
seed)`). Sale positions are `1 … totalTickets` (1 = the first ticket sold);
`units` is the multiset of prize-unit reference strings. Groups of identical
units are placed by the same partial Fisher–Yates over the free positions, so
every prize lands uniformly and without replacement. Where a round fixes in
advance which part of the sale some prizes fall in, that constraint is part of
the committed inputs (`rules`, digested into the commit) and prizes are placed
uniformly within it. The unit list is reconstructed from the published counts
in canonical (bytewise sorted) order — the same order the platform uses — so
no extra configuration needs to be published. See *Instant Prizes* below.

**Seed mix.** `finalSeed = SHA-256(localSeed ‖ beaconValue)` with both inputs
as raw bytes (hex-decoded from the bundle). A draw recorded without a beacon
(degraded mode) uses the local seed directly and is labelled as
platform-trusted by the verifier.

**Digests.** Every digest is SHA-256 over length-framed items: each string is
preceded by its byte length as an 8-byte big-endian prefix, removing
concatenation ambiguity. The pool digest sorts items first
(order-independent); the winners digest preserves order; the allocation digest
iterates ticket numbers in ascending numeric order, framing
`decimal(ticketNumber)` then the prize reference.

## Instant Prizes: sealed before the first sale, unchanged after

What matters for an instant prize is that nothing about it can change once
tickets are on sale. Before the first ticket is sold, the platform:

1. places every instant prize on a **sale position** (1 = the first ticket
   sold) with the seeded shuffle above;
2. derives the **ticket number** each position will show from a
   domain-separated sub-seed (`DisplayPermutation`: a full Fisher–Yates over
   `[1, M]` seeded with `SHA-256("drawproof/display/v2" ‖ seed)`);
3. writes the commitment to its ledger: the digest of the complete placement,
   the digest of the ticket numbering, the ticket population, the hash of the
   sealed seed and the beacon pulse mixed into it;
4. reads every minted ticket back against that commitment and records the
   result on the ledger as well (`VerifyRecord`), so the platform's own check
   that the database held the committed pattern *before* sales is itself
   verifiable.

Tickets are then issued strictly in position order. There is no randomness at
purchase: a ticket's prize was decided, sealed and checked before anyone could
buy it. At close of sales the seed is disclosed and the verifier reproduces
the whole placement and numbering from it; while the round is on sale the seed
stays sealed and only the commitment can be checked (`VerifyInstantV2Sealed`).

The verifier (`VerifyInstantV2FromSeed`) checks, in order: the committed input
digests, the local-seed hash, the beacon mix, the reproduction of the placement
from the seed, that the placement honours any constraint committed with it,
and the ticket numbering (plus the published ticket numbers when included).

### The public bundle: placement published, rules withheld

A platform may publish the sealed placement to players while keeping the
placement rules for its auditors. Such a **public bundle** carries the commit
and reveal records and the complete placement, but no `rules`; the commit's
`rulesDigest` still names them. The verifier recognises this (`rules-withheld`)
and checks what it can without the rules: that the published placement digests
to the value committed before the first sale, that the local seed hashes to its
commitment and mixes with the beacon into the final seed, and that the ticket
numbering derives from that seed. It does not re-run the placement algorithm,
so "placed uniformly within the rules" is established on the audit bundle, not
here. A bundle that withholds the rules must publish the placement itself:
unit counts alone (`units`) cannot be checked without the rules.

A large placement travels as a file instead of inline JSON, named in
`allocationFile` with `allocationCount` prizes: one prize per line, the sale
position, a space, and the prize-unit reference (`AllocationLine`). The WASM
takes it through `drawverifyAllocationFeed` the way it takes a pool file, and
the Go API through `AllocationFeed` and `VerifyInstantV2Placement`.

## What the verifier checks

| Check | Kinds | Proves |
|---|---|---|
| `local-seed-hash-matches-commit` | all | The revealed seed is the one committed in advance. |
| `beacon-mix` | all | The final seed is fixed by both the platform's seed and the public beacon value. |
| `beacon-pulse-matches-commit` | main, vault | The pulse used is the pulse named before the draw. |
| `future-pulse-anti-grind` | main, vault (future-pulse) | The pulse published after the commit — its value was unknowable when the platform committed. |
| `pool-digest-matches-commit` | main, vault | The entrant pool was frozen before the draw and unaltered. |
| `winners-reproduced` | main, vault | Re-running the algorithm reproduces the exact published winners. |
| `winner-digest-consistent` | main, vault | The winners list was not edited after recording. |
| `allocation-digest-matches-commit` | instant | The full placement was sealed before sales opened. On a bundle that publishes `units` (prize reference → count) instead of `allocation`, the placement is reproduced from the seed first and its digest compared. |
| `allocation-reproduced-from-seed` | instant | The entire position→prize placement re-derives from the disclosed seed. |
| `display-permutation-matches-commit` | instant | The ticket numbers players saw re-derive from the same seed and match the committed digest. |
| `population-committed` | instant | The number of tickets was fixed in the commitment before sales. |
| `rules-digest-matches-commit`, `rules-satisfied` | instant | Any constraint on where prizes fall was committed in advance, is published, and the placement honours it. |
| `rules-withheld` | instant (public bundle) | The bundle publishes the placement but not the rules; the placement is checked against the commitment, not re-run. Informational, always passes. |
| `mint-verified-before-sales` | instant | The minted tickets were read back against the commitment before sales opened, and that read-back is on the ledger. |
| `seed-sealed` | instant (on sale) | The seed is still sealed; only the commitment can be checked until close. |

For full independence, the web page additionally fetches the named pulse from
the public drand relay (`https://api.drand.sh/public/<pulse>`) and recomputes
the pulse's scheduled time from the chain's own `genesis_time` and `period` —
none of which involves the platform.

## Scope

This verifier proves the outcome is **reproducible** from the published data
and that the inputs are **bound** to the pre-draw commitment. The commit
records themselves live on the platform's immutable (immudb) ledger, whose
ordering and timestamps are additionally subject to independent assurance
review with direct verified-ledger access.

## License

MIT — see [LICENSE](LICENSE).
