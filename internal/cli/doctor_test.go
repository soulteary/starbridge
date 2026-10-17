package cli

import "testing"

func TestDoctorVMCLI(t *testing.T) {
	status, result := invoke(t, "--dry-run", "doctor", "--vm", "Windows test")
	if status != 0 {
		t.Fatal(result)
	}
	data := resultData(t, result)
	if data["vm"] != "Windows test" || data["ready"] != false || data["dry_run"] != true {
		t.Fatal(data)
	}
	for _, args := range [][]string{{"doctor", "--vm"}, {"doctor", "--vm="}, {"doctor", "--vm=-invalid"}} {
		status, result := invoke(t, args...)
		if status == 0 {
			t.Fatal(args, result)
		}
		assertCode(t, result, "INVALID_ARGUMENT")
	}
}
