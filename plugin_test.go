package implugin

import "testing"

func TestPluginIdentity(t *testing.T) {
	p := Plugin{}
	if p.Name() != "im" {
		t.Fatalf("unexpected plugin name %q", p.Name())
	}
	if p.MountPath() != "/" {
		t.Fatalf("unexpected mount path %q", p.MountPath())
	}
}
