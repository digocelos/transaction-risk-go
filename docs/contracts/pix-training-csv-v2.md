# CSV layout `pix-training-csv-v2`

**Status:** Normative for `pix-training-csv-v2`, implemented by the `trainingcsv` package and `cmd/convert`. It replaces [`pix-training-csv-v1`](pix-training-csv-v1.md), which `cmd/convert` no longer reads (ADR 0021, ADR 0024): one column is added, and the converted records are [`pix-training-example-v3`](pix-training-example-v3.md). Everything this document does not mention is as `pix-training-csv-v1` specifies it, read with the version 3 record names.

## 1. Columns

One column per value of a `pix-training-example-v3` record, named by its path as in [`pix-training-csv-v1` §3](pix-training-csv-v1.md#3-columns): the 97 columns of version 1 plus `observations.payer_account.initiated_outgoing_pix_24h.amount_cents`, an integer amount in cents, required whenever the observation is present, and 0 exactly when its `count` is 0. The [template](../../templates/training-data/pix-training-example-v3.csv) and its [guide](../../templates/training-data/README.md) list the 98 columns in the order of the contract.

## 2. Conversion

As [`pix-training-csv-v1` §5](pix-training-csv-v1.md#5-conversion): every record is validated with the strict `pix-training-example-v3` decoder and classified with `pix-risk-v3`. `training_example_version` is `pix-training-example-v3` and `contract_version` is `pix-payer-input-v3` for every record; neither has a column. A file in the version 1 layout is reported with `missing_column` for the new column.

## 3. Migration from version 1

Add the column and fill it for every row with a 24-hour history: the sum of the amounts the count counts, or 0 when the count is 0. A spreadsheet that does not keep those amounts cannot be converted to this version.
