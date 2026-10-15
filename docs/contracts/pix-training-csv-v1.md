# CSV layout `pix-training-csv-v1`

**Status:** Normative for `pix-training-csv-v1`, implemented by the `trainingcsv` package and `cmd/convert`.

## 1. Scope

`pix-training-csv-v1` is a spreadsheet layout for [`pix-training-example-v2`](pix-training-example-v2.md) records. It exists so that training data can be prepared in a spreadsheet; it is not read by `train` or `evaluate`. `cmd/convert` turns a CSV file in this layout into the JSON Lines dataset they read, record by record, and the dataset, not the CSV file, is what a run fingerprints (ADR 0021). The [template](../../templates/training-data/pix-training-example-v2.csv) and its [guide](../../templates/training-data/README.md) list every column.

## 2. File

- The file is UTF-8 text in the format of RFC 4180: rows separated by line breaks, cells separated by a comma or, with `-separator ';'`, by a semicolon, and cells containing the separator, a quote, or a line break enclosed in double quotes. A byte order mark at the start and CRLF line breaks are accepted, because spreadsheet tools write them.
- The first row is the header. It names every column of [section 3](#3-columns) exactly once, in any order, and no other column.
- Every other row is one record, with as many cells as the header. A row whose cells are all empty is skipped.
- `training_example_version` is `pix-training-example-v2` and `contract_version` is `pix-payer-input-v2` for every record; neither has a column.

## 3. Columns

There is one column for each value of a `pix-training-example-v2` record, named by its path with `.` between members. The `scoring_input.` prefix is left out, so `scoring_input.pix_payment.amount_cents` is `pix_payment.amount_cents`, while the outcome columns keep `outcome.` and the group key is `group_key`. The template and its guide list the 97 columns in the order of the contract, with their accepted values.

## 4. Values

| Value | Cell |
|---|---|
| `null` | Empty. An empty cell is never zero. |
| Integer | Its decimal digits, such as `250000`. A cell that is not a JSON number, such as `12,50`, is reported as the wrong type. |
| Any other value | Its text, without quotes, such as `2026-05-14T14:03:07.120-03:00`, `no_pix_key`, or `EVP`. |

**Observations.** An observation is present only when its `as_of` cell is filled. With an empty `as_of`, the observation is `null`, and every other cell of it must be empty; a value there is `value_without_as_of`.

**Subjects that do not apply.** A conditional subject is omitted when its discriminator says it does not apply: `observations.payer_device` with `pix_payment.payer_device_usage` `no_payer_device`, `observations.recipient_pix_key` with `pix_payment.recipient_key_usage` `no_pix_key`, and `observations.payer_account.period_limit` with `pix_payment.payer_limit_period` `no_period_limit`. Every cell of an omitted subject must be empty; a value there is `member_not_allowed`.

**Counters.** A DICT counter whose `d90`, `m12`, and `m60` cells are all empty is `null` as a whole; otherwise, each empty cell is a `null` window. `pix-risk-v1` treats both the same way.

## 5. Conversion

Every record is validated with the strict `pix-training-example-v2` decoder and classified with `pix-risk-v1`, as `train` would. The dataset is written only when every row is valid. Insufficient and immature records are valid and are written; `train` excludes them.

Each problem names the row, counting the header as row 1 and using the line where the row starts, the column, a code, and a message. The codes are those of the contracts, plus `invalid_csv`, `unknown_column`, `duplicate_column`, `missing_column`, `value_without_as_of`, and `member_not_allowed` for the layout itself. At most 1,000 problems are reported. More rows than the record limit, 1,000,000 by default, stop the conversion.

The records are written as compact JSON, one per line, with members in the order of the contract. Converting a dataset to this layout and back gives the same records.
