package dictionary

import "testing"

func TestFlattenPinyin(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ni3 hao3", "nihao"},
		{"Zhong1 guo2", "zhongguo"},
		{"lu:4 se4", "lvse"},
		{"nu:3 hai2", "nvhai"},
		{"hua1 r5", "huar"},
		{"Mao2 Ze2 dong1", "maozedong"},
		{"", ""},
	}
	for _, c := range cases {
		got := FlattenPinyin(c.in)
		if got != c.want {
			t.Errorf("FlattenPinyin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseLine(t *testing.T) {
	t.Run("standard entry", func(t *testing.T) {
		e, err := ParseLine("你好 你好 [ni3 hao3] /hello/hi/")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if e == nil {
			t.Fatal("got nil entry")
		}
		if e.Traditional != "你好" || e.Simplified != "你好" {
			t.Errorf("hanzi parse wrong: %+v", e)
		}
		if e.Pinyin != "ni3 hao3" {
			t.Errorf("pinyin = %q", e.Pinyin)
		}
		if e.PinyinFlat != "nihao" {
			t.Errorf("flat = %q", e.PinyinFlat)
		}
		if e.English != "hello/hi" {
			t.Errorf("english = %q", e.English)
		}
	})

	t.Run("traditional differs from simplified", func(t *testing.T) {
		e, err := ParseLine("中國 中国 [Zhong1 guo2] /China/")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if e.Traditional != "中國" || e.Simplified != "中国" {
			t.Errorf("got %+v", e)
		}
		if e.PinyinFlat != "zhongguo" {
			t.Errorf("flat = %q", e.PinyinFlat)
		}
	})

	t.Run("u-umlaut", func(t *testing.T) {
		e, err := ParseLine("綠 绿 [lu:4] /green/")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if e.PinyinFlat != "lv" {
			t.Errorf("flat = %q, want lv", e.PinyinFlat)
		}
	})

	t.Run("comment line", func(t *testing.T) {
		e, err := ParseLine("# CC-CEDICT")
		if err != nil || e != nil {
			t.Errorf("comment: got entry=%+v err=%v", e, err)
		}
	})

	t.Run("blank line", func(t *testing.T) {
		e, err := ParseLine("   ")
		if err != nil || e != nil {
			t.Errorf("blank: got entry=%+v err=%v", e, err)
		}
	})

	t.Run("missing brackets", func(t *testing.T) {
		_, err := ParseLine("你好 你好 ni3 hao3 /hello/")
		if err == nil {
			t.Error("expected error for missing brackets")
		}
	})

	t.Run("multiple definitions preserved", func(t *testing.T) {
		e, err := ParseLine("好 好 [hao3] /good/well/proper/")
		if err != nil {
			t.Fatal(err)
		}
		if e.English != "good/well/proper" {
			t.Errorf("english = %q", e.English)
		}
	})

	t.Run("trailing CR survives", func(t *testing.T) {
		e, err := ParseLine("你好 你好 [ni3 hao3] /hello/\r")
		if err != nil {
			t.Fatal(err)
		}
		if e.English != "hello" {
			t.Errorf("english = %q", e.English)
		}
	})
}
