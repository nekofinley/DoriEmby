package metadata

import "testing"

// The matcher must accept real titles and reject "close enough" nonsense —
// a wrong rename is worse than no metadata at all.
func TestPickBGM(t *testing.T) {
	subj := func(cn, orig string, year int) BgmSubject {
		return BgmSubject{NameCN: cn, Name: orig, Year: year}
	}
	cases := []struct {
		name    string
		query   string
		year    int
		results []BgmSubject
		want    int
	}{
		{
			name:    "中文名精确匹配",
			query:   "孤独摇滚",
			results: []BgmSubject{subj("孤独摇滚！", "ぼっち・ざ・ろっく！", 2022), subj("孤独", "", 1959)},
			want:    0,
		},
		{
			name:    "日文原名匹配",
			query:   "ぼっち・ざ・ろっく",
			results: []BgmSubject{subj("孤独摇滚！", "ぼっち・ざ・ろっく！", 2022)},
			want:    0,
		},
		{
			name:    "去掉标点后精确",
			query:   "86 不存在的战区",
			results: []BgmSubject{subj("86-不存在的战区-", "86-エイティシックス-", 2021)},
			want:    0,
		},
		{
			name:    "续作不会被主条目抢走(精确优先)",
			query:   "咒术回战",
			results: []BgmSubject{subj("咒术回战", "呪術廻戦", 2020), subj("咒术回战 怀玉･玉折 / 涩谷事变", "", 2023)},
			want:    0,
		},
		{
			name:    "虚构电影名不能变成 Another",
			query:   "Mankai Movie A3!: Another Stories",
			year:    2021,
			results: []BgmSubject{subj("Another", "アナザー", 2012)},
			want:    -1,
		},
		{
			name:    "非番剧名字不能乱配",
			query:   "Dette er en test show",
			results: []BgmSubject{subj("Computer Test", "", 2020)},
			want:    -1,
		},
		{
			name:    "真人电影名不该匹配到无关番剧",
			query:   "流浪地球",
			results: []BgmSubject{subj("来自深渊 漂泊之黄昏", "メイドインアビス", 2019)},
			want:    -1,
		},
		{
			name:    "没有结果",
			query:   "某个不存在的名字",
			results: nil,
			want:    -1,
		},
		{
			name:    "罗马音目录名暂时匹配不到(已知取舍,交给 TMDB)",
			query:   "Kekkai Sensen",
			results: []BgmSubject{subj("血界戦線", "血界戦線", 2015)},
			want:    -1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickBGM(c.query, c.year, c.results); got != c.want {
				t.Fatalf("pickBGM(%q) = %d, want %d", c.query, got, c.want)
			}
		})
	}
}

func TestPreferCN(t *testing.T) {
	if got := preferCN("孤独摇滚！", "ぼっち・ざ・ろっく！"); got != "孤独摇滚！" {
		t.Fatalf("preferCN = %q", got)
	}
	if got := preferCN("", "Kekkai Sensen"); got != "Kekkai Sensen" {
		t.Fatalf("preferCN fallback = %q", got)
	}
}
