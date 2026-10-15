# Training data can be prepared as CSV and converted to JSON Lines

Real training data will be assembled by people, often in a spreadsheet, while `train` and `evaluate` read `pix-training-example-v2` JSON Lines, whose nested objects separate missing observations (`null`) from subjects that do not apply (omitted). The CSV layout `pix-training-csv-v1` flattens a record into one column per value and keeps both distinctions with two rules: an observation exists only when its `as_of` is filled, and a subject is omitted when its discriminator says it does not apply. `cmd/convert` turns a filled CSV file into JSON Lines, validating every record as `train` would and reporting each problem by row and column.

The JSON Lines dataset stays the only input of training and evaluation, and the only fingerprinted file: the conversion is a separate, checked step, and a run is identified by the exact dataset it read. The template is versioned in `templates/training-data/`, and `data/` is ignored by git, so following the guide never commits real payment data.

## Considered options

- **`train` and `evaluate` read CSV directly:** one step less, but two formats would be fingerprinted and diagnosed, and the CSV layout would become an input contract of training.
- **A CSV with only the required facts:** easier to fill, but it would drop the device, limit, Pix key, and DICT observations that most features use.
- **A JSON Lines template only:** no new format, but nested JSON is hard to fill by hand or in a spreadsheet.

## Consequences

- The CSV layout follows `pix-training-example-v2`: a new training example version needs a new CSV layout version.
- Converting a dataset to CSV and back gives the same records, which the tests check over the specification examples and a synthetic dataset.
