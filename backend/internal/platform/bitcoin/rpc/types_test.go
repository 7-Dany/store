package rpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── BtcToSat precision (table-driven) ─────────────────────────────────────────

func TestBtcToSat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   btcRawAmount
		wantSat int64
		wantErr string
	}{
		// Valid amounts
		{"zero", 0, 0, ""},
		{"one_satoshi", 0.00000001, 1, ""},
		{"small_amount", 0.00005, 5_000, ""},
		{"invoice_amount", 0.00123456, 123_456, ""},
		{"point_one", 0.1, 10_000_000, ""},
		{"full_8dp", 0.12345678, 12_345_678, ""},
		{"one_btc", 1.0, 100_000_000, ""},
		{"max_supply_approx", 20_999_999.9769, 2_099_999_997_690_000, ""},
		{"exact_21m_rejected", 21_000_000, 0, "exceeds the maximum Bitcoin supply"},

		// Sub-satoshi edge cases
		{"sub_satoshi_0.1_sat", 0.000000001, 0, "below 1 satoshi"},
		{"sub_satoshi_0.4_sat", 0.000000004, 0, "below 1 satoshi"},
		{"half_satoshi_rounds_up", 0.000000005, 1, ""},        // math.Round(0.5) = 1
		{"sub_satoshi_0.9_sat_rounds_up", 0.000000009, 1, ""}, // math.Round(0.9) = 1
		{"nine_dp_rounds_up", 0.999999999, 100_000_000, ""},   // rounds to 1 BTC

		// Errors
		{"negative", -0.001, 0, "negative amount"},
		{"above_max", 21_000_001, 0, "exceeds the maximum Bitcoin supply"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sat, err := BtcToSat(tc.input)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSat, sat)
		})
	}
}

func TestBtcToSatSigned(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   btcRawAmount
		wantSat int64
		wantErr string
	}{
		{"positive", 0.001, 100_000, ""},
		{"negative", -0.001, -100_000, ""},
		{"zero", 0, 0, ""},
		{"negative_one_satoshi", -0.00000001, -1, ""},
		{"negative_sub_satoshi", -0.000000001, 0, "below 1 satoshi"},
		{"negative_exceeds_max", -21_000_000, 0, "exceeds the maximum Bitcoin supply"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sat, err := BtcToSatSigned(tc.input)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSat, sat)
		})
	}
}

func TestFeeRateToSatPerVB(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   btcRawAmount
		wantSat int64
		wantErr string
	}{
		{"one_sat_per_vb", 0.00001000, 1, ""},  // 1000 sat/kvB / 1000 = 1 sat/vB
		{"ten_sat_per_vb", 0.00010000, 10, ""}, // 10000 sat/kvB / 1000 = 10 sat/vB
		{"zero", 0, 0, ""},
		{"negative", -0.00001000, 0, "lacks sufficient data"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sat, err := FeeRateToSatPerVB(tc.input)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSat, sat)
		})
	}
}

func TestBtcToSatOptional(t *testing.T) {
	t.Parallel()

	t.Run("nil_returns_zero", func(t *testing.T) {
		t.Parallel()
		sat, err := BtcToSatOptional(nil)
		require.NoError(t, err)
		assert.Equal(t, int64(0), sat)
	})

	t.Run("non_nil_converts", func(t *testing.T) {
		t.Parallel()
		amount := btcRawAmount(0.001)
		sat, err := BtcToSatOptional(&amount)
		require.NoError(t, err)
		assert.Equal(t, int64(100_000), sat)
	})
}

// ── FeeEstimate.HasEstimate ───────────────────────────────────────────────────

func TestFeeEstimate_HasEstimate_True(t *testing.T) {
	t.Parallel()
	f := FeeEstimate{FeeRate: 0.00010000, Blocks: 3}
	assert.True(t, f.HasEstimate())
}

func TestFeeEstimate_HasEstimate_False_NegativeFeeRate(t *testing.T) {
	t.Parallel()
	f := FeeEstimate{FeeRate: -1, Blocks: 0, Errors: []string{"Insufficient data"}}
	assert.False(t, f.HasEstimate())
}

func TestFeeEstimate_HasEstimate_False_HasErrors(t *testing.T) {
	t.Parallel()
	f := FeeEstimate{FeeRate: 0.0001, Blocks: 3, Errors: []string{"some warning"}}
	assert.False(t, f.HasEstimate())
}

func TestFeeRateToSatPerVB_NegativeFeeRate_ReturnsSentinel(t *testing.T) {
	t.Parallel()
	_, err := FeeRateToSatPerVB(-1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoFeeEstimate)
}

// ── BtcToSat supply cap ──────────────────────────────────────────────────────

func TestBtcToSat_AboveMaxSupply_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := BtcToSat(21_000_001)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum Bitcoin supply")
}

func TestBtcToSat_ExactMaxSupply_ReturnsError(t *testing.T) {
	t.Parallel()
	// 21_000_000 is now rejected with >= (was previously accepted with >).
	_, err := BtcToSat(21_000_000)
	require.Error(t, err, "exactly 21M BTC should be rejected as it exceeds the real supply cap")
	assert.Contains(t, err.Error(), "maximum Bitcoin supply")
}

// ── Fuzz targets ──────────────────────────────────────────────────────────────

func FuzzBtcToSat(f *testing.F) {
	f.Add(0.1)
	f.Add(0.0)
	f.Add(-1.0)
	f.Add(21000001.0)
	f.Add(0.00000001)
	f.Add(0.000000005)
	f.Add(20999999.9769)

	f.Fuzz(func(t *testing.T, btc float64) {
		sat, err := BtcToSat(btcRawAmount(btc))
		if err == nil {
			require.GreaterOrEqual(t, sat, int64(0))
			require.LessOrEqual(t, sat, int64(2_100_000_000_000_000))
		} else {
			require.Equal(t, int64(0), sat)
		}
	})
}

func FuzzBtcToSatSigned(f *testing.F) {
	f.Add(0.1)
	f.Add(-0.1)
	f.Add(0.0)
	f.Add(-21000000.0)
	f.Add(-0.00000001)

	f.Fuzz(func(t *testing.T, btc float64) {
		sat, err := BtcToSatSigned(btcRawAmount(btc))
		if err == nil {
			if btc < 0 {
				require.LessOrEqual(t, sat, int64(0))
			} else {
				require.GreaterOrEqual(t, sat, int64(0))
			}
		} else {
			require.Equal(t, int64(0), sat)
		}
	})
}

func FuzzFeeRateToSatPerVB(f *testing.F) {
	f.Add(0.00001)
	f.Add(0.0)
	f.Add(-0.00001)
	f.Add(0.001)

	f.Fuzz(func(t *testing.T, rate float64) {
		sat, err := FeeRateToSatPerVB(btcRawAmount(rate))
		if err == nil {
			require.GreaterOrEqual(t, sat, int64(0))
		} else {
			require.Equal(t, int64(0), sat)
		}
	})
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkBtcToSat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = BtcToSat(0.12345678)
	}
}

func BenchmarkBtcToSatSigned(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = BtcToSatSigned(-0.12345678)
	}
}
