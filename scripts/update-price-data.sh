#!/bin/sh
# Regenerate the embedded price table from the three datasets (ADR 0007). The
# weekly price-data workflow runs this, and a maintainer can too. This is the
# only place price data is downloaded: kman itself never fetches it.
#
#   sh scripts/update-price-data.sh
#
# It leaves the table as it is when no price changed.
set -eu

cd "$(dirname "$0")/.."
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT

fetch() {
	curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
		--retry 3 --max-time 120 --output "$dir/$1" "$2"
}
fetch litellm.json https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json
fetch genai.json https://raw.githubusercontent.com/pydantic/genai-prices/main/prices/new_data/v2/data.json
fetch modelsdev.json https://models.dev/api.json

go run ./internal/pricing/pricegen generate \
	--litellm "$dir/litellm.json" --genai "$dir/genai.json" --modelsdev "$dir/modelsdev.json"
