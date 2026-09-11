package domain

import "testing"

func TestUserCanEstablishSession(t *testing.T) {
	cases := []struct {
		name   string
		user   *User
		want   bool
	}{
		{name: "nil", want: false},
		{name: "active", user: &User{Status: StatusActive}, want: true},
		{name: "pending", user: &User{Status: StatusPending}, want: true},
		{name: "disabled", user: &User{Status: StatusDisabled}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.user.CanEstablishSession(); got != test.want {
				t.Fatalf("CanEstablishSession() = %v, want %v", got, test.want)
			}
		})
	}
}
