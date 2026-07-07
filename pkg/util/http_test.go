package util

import "testing"

func TestValidatePort(t *testing.T) {
	cases := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid port", 8000, false},
		{"privileged port", 80, false},
		{"max port", 65535, false},
		{"zero port", 0, true},
		{"negative port", -1, true},
		{"port too large", 65536, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePort(PortInput{Name: "test", Port: c.port})
			if (err != nil) != c.wantErr {
				t.Errorf("ValidatePort(%d) error = %v, wantErr %v", c.port, err, c.wantErr)
			}
		})
	}
}

func TestValidatePortMultiple(t *testing.T) {
	err := ValidatePort(
		PortInput{Name: "a", Port: 8000},
		PortInput{Name: "b", Port: 0},
	)
	if err == nil {
		t.Error("expected error when one of the ports is invalid")
	}
}
