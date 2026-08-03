package platform

import "testing"

func TestParsesCompilerTokens(t *testing.T) {
	if c, ok := WindowsCompilerFromToken("VC15"); !ok || c != Vc15 {
		t.Errorf("VC15 -> %v ok=%v", c, ok)
	}
	if c, ok := WindowsCompilerFromToken("vs17"); !ok || c != Vs17 {
		t.Errorf("vs17 -> %v ok=%v", c, ok)
	}
	if _, ok := WindowsCompilerFromToken("NTS"); ok {
		t.Error("NTS should not parse")
	}
	if Vs17.Token() != "vs17" {
		t.Errorf("Vs17 token = %q", Vs17.Token())
	}
}

func TestParsesFromPhpinfo(t *testing.T) {
	info := "PHP Extension Build => API20240924,NTS,VS17\nfoo => bar"
	c := CompilerFromPhpinfo(info)
	if c == nil || *c != Vs17 {
		t.Errorf("got %v, want Vs17", c)
	}
	ts := "PHP Extension Build => API20220829,TS,VC15"
	c = CompilerFromPhpinfo(ts)
	if c == nil || *c != Vc15 {
		t.Errorf("got %v, want Vc15", c)
	}
	if CompilerFromPhpinfo("nothing here") != nil {
		t.Error("expected nil")
	}
}
