package store

import "time"

// InstallScope is the scope of an override for the whole install. Any other
// scope is an upstream's id.
const InstallScope = "install"

// PriceOverride is one row of the price_overrides table. Prices are
// microdollars per million tokens. The admin module owns the shape of
// ExtraJSON, which holds the optional prices.
type PriceOverride struct {
	Scope      string
	Model      string
	Input      int64
	Output     int64
	MaxOutput  int64
	ExtraJSON  string
	Actor      string // who last set it
	UpdatedAt  time.Time
	Generation int64 // set by the store
}

const priceOverrideColumns = `scope, model, input, output, max_output, extra_json, actor, updated_at, generation`

func scanPriceOverride(row interface{ Scan(...any) error }) (PriceOverride, error) {
	var o PriceOverride
	var updated int64
	err := row.Scan(&o.Scope, &o.Model, &o.Input, &o.Output, &o.MaxOutput, &o.ExtraJSON, &o.Actor, &updated, &o.Generation)
	o.UpdatedAt = fromMilli(updated)
	return o, err
}

// PutPriceOverride sets the override of o.Model in o.Scope with this
// transaction's generation, and returns the row as stored.
func (tx *Tx) PutPriceOverride(o PriceOverride) (PriceOverride, error) {
	gen, err := tx.Generation()
	if err != nil {
		return PriceOverride{}, err
	}
	out, err := scanPriceOverride(tx.tx.QueryRowContext(tx.ctx, `INSERT INTO price_overrides (`+priceOverrideColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, model) DO UPDATE SET input = excluded.input, output = excluded.output, max_output = excluded.max_output,
			extra_json = excluded.extra_json, actor = excluded.actor, updated_at = excluded.updated_at, generation = excluded.generation
		RETURNING `+priceOverrideColumns,
		o.Scope, o.Model, o.Input, o.Output, o.MaxOutput, o.ExtraJSON, o.Actor, toMilli(o.UpdatedAt), gen))
	if err != nil {
		return PriceOverride{}, typed(err, "put price override "+o.Model)
	}
	return out, nil
}

// PriceOverride returns the override of model in scope.
func (tx *Tx) PriceOverride(scope, model string) (PriceOverride, error) {
	o, err := scanPriceOverride(tx.tx.QueryRowContext(tx.ctx, `SELECT `+priceOverrideColumns+` FROM price_overrides WHERE scope = ? AND model = ?`, scope, model))
	if err != nil {
		return PriceOverride{}, typed(err, "price override "+model)
	}
	return o, nil
}

// PriceOverrides returns every override, by scope and model.
func (tx *Tx) PriceOverrides() ([]PriceOverride, error) {
	rows, err := tx.tx.QueryContext(tx.ctx, `SELECT `+priceOverrideColumns+` FROM price_overrides ORDER BY scope, model`)
	if err != nil {
		return nil, typed(err, "list price overrides")
	}
	defer rows.Close()
	var out []PriceOverride
	for rows.Next() {
		o, err := scanPriceOverride(rows)
		if err != nil {
			return nil, typed(err, "list price overrides")
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, typed(err, "list price overrides")
	}
	return out, nil
}

// DeletePriceOverride removes the override of model in scope. The row is gone
// with no tombstone: a later change feed has to carry deletes itself.
func (tx *Tx) DeletePriceOverride(scope, model string) error {
	if _, err := tx.Generation(); err != nil {
		return err
	}
	var gone string
	err := tx.tx.QueryRowContext(tx.ctx, `DELETE FROM price_overrides WHERE scope = ? AND model = ? RETURNING model`, scope, model).Scan(&gone)
	if err != nil {
		return typed(err, "delete price override "+model)
	}
	return nil
}
