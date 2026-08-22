package windowsx

import (
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestDecodePowerShellJSONCleanArray(t *testing.T) {
	var got []model.RemoteAppStatus
	if err := decodePowerShellJSON(`[{"alias":"Calc","published":true}]`, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Alias != "Calc" || !got[0].Published {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestDecodePowerShellJSONSingleObject(t *testing.T) {
	var got []model.RemoteAppStatus
	if err := decodePowerShellJSON(`{"alias":"Calc","published":true}`, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Alias != "Calc" {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestDecodePowerShellJSONIgnoresCLIXMLNoise(t *testing.T) {
	raw := "#< CLIXML\r\n" +
		`[{"resource_id":"281750fc5851c7fd","alias":"Rechner","display_name":"Rechner","path":"C:\\Windows\\system32\\win32calc.exe","path_exists":true,"published":true,"managed":true,"in_sync":true}]` +
		"\r\nSystem.Management.Automation.PSCustomObjectSystem.Object1Module werden fuer erstmalige Verwendung vorbereitet."
	var got []model.RemoteAppStatus
	if err := decodePowerShellJSON(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Alias != "Rechner" || got[0].Path != `C:\Windows\system32\win32calc.exe` {
		t.Fatalf("unexpected result: %#v", got)
	}
}
