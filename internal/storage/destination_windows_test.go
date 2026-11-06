package storage

import "testing"

func TestDestinationKindDesktopPrecedesDrive(t *testing.T) {
	for _, test := range []struct{ target, desktop, want string }{
		{`C:\Users\Demo\Desktop`, `c:\users\demo\desktop`, "desktop"},
		{`C:\Users\Demo\Desktop\Output`, `C:\Users\Demo\Desktop`, "desktop"},
		{`C:\Users\Demo\DesktopOther`, `C:\Users\Demo\Desktop`, "drive_c"},
		{`C:\`, `C:\Users\Demo\Desktop`, "drive_c"},
		{`D:\Redirected\Desktop\Output`, `D:\Redirected\Desktop`, "desktop"},
		{`D:\Media`, `D:\Redirected\Desktop`, ""},
	} {
		if got := destinationKind(test.target, test.desktop); got != test.want {
			t.Errorf("%s: %q, want %q", test.target, got, test.want)
		}
	}
}
