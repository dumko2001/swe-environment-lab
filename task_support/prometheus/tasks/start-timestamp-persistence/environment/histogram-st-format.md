# Histogram start-timestamp storage contract

This experimental format must interoperate with existing downstream readers.
Implementation structure and internal type names are unrestricted.

## Chunk format

Encoding tag `5` denotes integer histograms; tag `6` denotes float histograms.
Both extend their existing histogram payload format with this three-byte header:

- Byte 0 bits 7–6 retain the existing counter-reset header values.
- Byte 0 bits 5–0 and byte 1 form a big-endian 14-bit sample count.
- Byte 2 bit 7 indicates a known first start timestamp (ST); bits 6–0 encode
  the zero-based sample index of the first ST change. Zero means no change.

The normal histogram sample payload is followed immediately by its ST payload
at the current bit position, without padding. All existing bucket, schema,
staleness, and counter/gauge semantics remain applicable.

## ST payload

ST zero means unknown. For sample zero, write a Go signed varint of `timestamp
- ST` only when the header marks its ST as known. For subsequent samples before
the first change, no ST payload is present. At the first change, write
`previous sample timestamp - current ST` using the repository's existing signed
varbit representation. For every later sample, write the delta of that value
from the preceding encoded value, using the same representation.

If no ST change occurred before zero-based sample index 127, index 127 starts
this encoding anyway and is recorded in the header. The first-change index is
relative to each chunk; ST values continue to describe the original samples
when chunks split, recode, merge, or compact.

## Compatibility

The feature is disabled by default and independent of `xor2-encoding`. Existing
histogram chunks remain readable. Enabled readers must recover samples from
self-contained TSDB blocks after the writer exits, without WAL, head files, or
auxiliary state. Constant start timestamps must add bounded storage overhead: for a 130-sample
series, the enabled compacted block must be no more than 64 bytes larger than
the equivalent block written with the feature disabled. Internal class layout
is unrestricted.

Expose the flag’s enabled state as `tsdb.histograms_st_encoding` through the existing `/api/v1/features` endpoint, independently of `tsdb.xor2_encoding`.
