package i18n

import "testing"

func expectPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("%s: expected panic, got none", name)
		}
	}()
	fn()
}

func TestRegisterTableGuards(t *testing.T) {
	expectPanic(t, "empty key", func() {
		registerTable(map[string][2]string{
			"   ": {"EN", "ZH"},
		})
	})

	expectPanic(t, "empty EN", func() {
		registerTable(map[string][2]string{
			"test.register_table.empty_en": {"", "ZH"},
		})
	})

	expectPanic(t, "empty ZH", func() {
		registerTable(map[string][2]string{
			"test.register_table.empty_zh": {"EN", ""},
		})
	})

	expectPanic(t, "duplicate key", func() {
		registerTable(map[string][2]string{
			"common.settings": {"Settings", "设置"},
		})
	})

	const tempKey = "test.register_table.valid"
	registerTable(map[string][2]string{
		tempKey: {"Valid", "有效"},
	})
	defer func() {
		delete(enTable, tempKey)
		delete(zhTable, tempKey)
	}()

	if !HasKey(tempKey) {
		t.Fatalf("expected %q to be registered", tempKey)
	}
}
