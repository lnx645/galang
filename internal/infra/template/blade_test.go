package template

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tulis membuat file template sementara dan mengembalikan Engine.
func tulis(t *testing.T, files map[string]string) *Engine {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := dir + "/" + name
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := tulisFile(path, src); err != nil {
			t.Fatalf("tulis %s: %v", name, err)
		}
	}
	return NewEngine(dir)
}

func tulisFile(path, src string) error {
	return os.WriteFile(path, []byte(src), 0o644)
}

// render helper.
func render(t *testing.T, e *Engine, name string, data map[string]interface{}) string {
	t.Helper()
	out, err := e.Render(name, data)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out
}

func TestStandaloneDanEscape(t *testing.T) {
	e := tulis(t, map[string]string{
		"satu.blade": `<h1>{{ $judul }}</h1>` + "\n{{-- komentar --}}\n" +
			`<p>{{ $aman }}</p><b>{!! $mentah !!}</b>`,
	})
	out := render(t, e, "satu.blade", map[string]interface{}{
		"judul": "Halo", "aman": "<script>", "mentah": "<i>ok</i>",
	})
	if strings.Contains(out, "komentar") {
		t.Errorf("komentar blade harus dibuang: %q", out)
	}
	if !strings.Contains(out, "<h1>Halo</h1>") {
		t.Errorf("judul hilang: %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("{{ }} harus escape: %q", out)
	}
	if !strings.Contains(out, "<b><i>ok</i></b>") {
		t.Errorf("{!! !!} harus mentah: %q", out)
	}
}

func TestExtendsSectionYield(t *testing.T) {
	e := tulis(t, map[string]string{
		"layouts/app.blade": `<title>@yield("judul", "Default Judul")</title>` +
			`<main>@yield("isi")</main><script>@yield("skrip")</script>`,
		"halo.blade": `@extends("layouts/app")

@section("judul", "Halo Duniaku")
@section("isi")
<p>Selamat datang, {{ $nama }}!</p>
@endsection
`,
	})
	out := render(t, e, "halo.blade", map[string]interface{}{"nama": "Budi"})
	if !strings.Contains(out, "<title>Halo Duniaku</title>") {
		t.Errorf("section dua argumen harus menang atas default yield: %q", out)
	}
	if !strings.Contains(out, "<p>Selamat datang, Budi!</p>") {
		t.Errorf("isi section hilang: %q", out)
	}
	if !strings.Contains(out, "<script></script>") {
		t.Errorf("yield tanpa section harus kosong, bukan error: %q", out)
	}
	if strings.Contains(out, "@") && (strings.Contains(out, "@section") || strings.Contains(out, "@yield")) {
		t.Errorf("sisa direktif tidak terkonversi: %q", out)
	}
}

func TestYieldDefaultDipakaiSaatChildTidakDefinisikan(t *testing.T) {
	e := tulis(t, map[string]string{
		"layouts/base.blade": `<title>@yield("judul", "Tanpa Judul")</title>@yield("isi")`,
		"polos.blade":        `@extends("layouts/base")`,
	})
	out := render(t, e, "polos.blade", nil)
	if !strings.Contains(out, "<title>Tanpa Judul</title>") {
		t.Errorf("default @yield harus terpakai: %q", out)
	}
}

func TestSectionChildMenangAtasDefaultLayout(t *testing.T) {
	e := tulis(t, map[string]string{
		"layouts/base.blade": `@section("aksi", "Tombol Layout")@yield("aksi")@yield("isi")`,
		"child.blade": `@extends("layouts/base")` +
			`@section("aksi", "Tombol Child")@section("isi")X@endsection`,
	})
	out := render(t, e, "child.blade", nil)
	if !strings.Contains(out, "Tombol Child") || strings.Contains(out, "Tombol Layout") {
		t.Errorf("section child harus menang: %q", out)
	}
}

func TestInclude(t *testing.T) {
	e := tulis(t, map[string]string{
		"layouts/app.blade": `<body>@include("partials/nav")@yield("isi")</body>`,
		"partials/nav.blade": `<nav>{{ $brand }}</nav>`,
		"utama.blade": `@extends("layouts/app")@section("isi")<p>isi</p>@endsection`,
	})
	out := render(t, e, "utama.blade", map[string]interface{}{"brand": "GaLang"})
	if !strings.Contains(out, "<nav>GaLang</nav>") {
		t.Errorf("partial tidak termuat: %q", out)
	}
	if !strings.Contains(out, "<p>isi</p>") {
		t.Errorf("isi hilang: %q", out)
	}
}

func TestForeachBindingDanElse(t *testing.T) {
	e := tulis(t, map[string]string{
		"daftar.blade": `@foreach($buah as $b)<i>{{ $b }}</i>@endforeach`,
		"kosong.blade": `@foreach($buah as $b)<i>{{ $b }}</i>@else<span>tidak ada</span>@endforeach`,
		"objek.blade":  `@foreach($orang as $o){{ $o.nama }}@endforeach`,
		"kunci.blade":  `@foreach($umur as $u => $v){{ $u }}={{ $v }};@endforeach`,
	})
	out := render(t, e, "daftar.blade", map[string]interface{}{"buah": []interface{}{"apel", "jeruk"}})
	if !strings.Contains(out, "<i>apel</i><i>jeruk</i>") {
		t.Errorf("foreach tidak mem-bind variabel: %q", out)
	}

	out = render(t, e, "kosong.blade", map[string]interface{}{"buah": []interface{}{}})
	if !strings.Contains(out, "tidak ada") {
		t.Errorf("@else foreach (kosong) tidak jalan: %q", out)
	}

	out = render(t, e, "objek.blade", map[string]interface{}{
		"orang": []interface{}{
			map[string]interface{}{"nama": "Ani"},
			map[string]interface{}{"nama": "Budi"},
		},
	})
	if !strings.Contains(out, "AniBudi") {
		t.Errorf("field objek dalam foreach: %q", out)
	}

	out = render(t, e, "kunci.blade", map[string]interface{}{
		"umur": map[string]interface{}{"Ani": 20, "Budi": 21},
	})
	if !strings.Contains(out, "=") || !strings.Contains(out, ";") {
		t.Errorf("foreach kunci=>nilai: %q", out)
	}
}

func TestScopeTerikatTidakBocorKeluarForeach(t *testing.T) {
	e := tulis(t, map[string]string{
		"campur.blade": `<p>{{ $judul }}</p>@foreach($xs as $x){{ $x }}@endforeach<p>{{ $judul }}</p>`,
	})
	out := render(t, e, "campur.blade", map[string]interface{}{
		"judul": "Judul", "xs": []interface{}{"1"},
	})
	if strings.Count(out, "<p>Judul</p>") != 2 {
		t.Errorf("$judul di luar foreach harus tetap .judul: %q", out)
	}
	if !strings.Contains(out, ">1</p>") && !strings.Contains(out, "1") {
		t.Errorf("isi foreach hilang: %q", out)
	}
}

func TestIfElseifElseDanOperator(t *testing.T) {
	e := tulis(t, map[string]string{
		"kondisi.blade": `@if($x > 3)<b>besar</b>@elseif($x == 2)<b>dua</b>@else<small>kecil</small>@endif`,
		"danor.blade":   `@if($a && !$b)<i>ya</i>@else<i>tidak</i>@endif`,
		"panggil.blade": `@if(count($daftar) > 0)<i>ada</i>@endif`,
	})
	out := render(t, e, "kondisi.blade", map[string]interface{}{"x": 5})
	if !strings.Contains(out, "<b>besar</b>") {
		t.Errorf("if komparasi ($x > 3) harus jalan: %q", out)
	}
	out = render(t, e, "kondisi.blade", map[string]interface{}{"x": 2})
	if !strings.Contains(out, "<b>dua</b>") {
		t.Errorf("elseif ($x == 2): %q", out)
	}
	out = render(t, e, "kondisi.blade", map[string]interface{}{"x": 1})
	if !strings.Contains(out, "<small>kecil</small>") {
		t.Errorf("else: %q", out)
	}

	out = render(t, e, "danor.blade", map[string]interface{}{"a": true, "b": false})
	if !strings.Contains(out, "<i>ya</i>") {
		t.Errorf("&& dan ! : %q", out)
	}
	out = render(t, e, "panggil.blade", map[string]interface{}{
		"daftar": []interface{}{"x"},
	})
	if !strings.Contains(out, "<i>ada</i>") {
		t.Errorf("count() > 0 dengan kurung bersarang: %q", out)
	}
}

func TestAritmetikaDanPipe(t *testing.T) {
	e := tulis(t, map[string]string{
		"hitung.blade": `{{ $a + $b }}|{{ $a * 2 }}|{{ $sapaan + "!" }}|{{ $x | upper }}`,
	})
	out := render(t, e, "hitung.blade", map[string]interface{}{
		"a": 7, "b": 5, "sapaan": "Halo", "x": "kecil",
	})
	if !strings.Contains(out, "12|14|Halo!|KECIL") {
		t.Errorf("aritmetika/pipe: %q", out)
	}
}

func TestStringDalamEkspresiTidakKonversiVariabel(t *testing.T) {
	e := tulis(t, map[string]string{
		"teks.blade": `{{ $judul + " $bukanVariabel && bukan" }}`,
	})
	out := render(t, e, "teks.blade", map[string]interface{}{"judul": "A"})
	// & dalam teks HTML di-escape oleh html/template → &amp;&amp;
	if !strings.Contains(out, "$bukanVariabel &amp;&amp; bukan") {
		t.Errorf("isi string literal harus utuh: %q", out)
	}
}

func TestSiklusExtendsDitolak(t *testing.T) {
	e := tulis(t, map[string]string{
		"a.blade": `@extends("b")`,
		"b.blade": `@extends("a")`,
	})
	if _, err := e.Render("a.blade", nil); err == nil {
		t.Errorf("siklus @extends harus menghasilkan error")
	}
}

func TestTemplateHilangDitolak(t *testing.T) {
	e := tulis(t, map[string]string{"ada.blade": "x"})
	if _, err := e.Render("tidakada.blade", nil); err == nil {
		t.Errorf("template tidak ada harus error")
	}
}

func TestIncludeDenganDataObject(t *testing.T) {
	// Literal object diterjemahkan ke pemanggilan dict — template Go
	// tidak punya literal object.
	e := tulis(t, map[string]string{
		"p.blade": `Hai {{ $nama }}, skor {{ $skor }}!`,
		"u.blade": `@include("p", {nama: "Budi", skor: 99})`,
	})
	out := render(t, e, "u.blade", nil)
	if !strings.Contains(out, "Hai Budi, skor 99!") {
		t.Errorf("include dengan data object: %q", out)
	}
}

func TestTeksBiasaTidakKonversiVariabel(t *testing.T) {
	// $var di luar aksi {{ }} adalah teks biasa (sama seperti Blade asli).
	e := tulis(t, map[string]string{
		"t.blade": `<p>harga $x dan {{ $x }}</p>`,
	})
	out := render(t, e, "t.blade", map[string]interface{}{"x": 7})
	if !strings.Contains(out, "<p>harga $x dan 7</p>") {
		t.Errorf("teks biasa $x harus utuh: %q", out)
	}
}

func TestDefinisiBertumpukPrioritasUrutanParse(t *testing.T) {
	// Guard semantik yang diandalkan implementasi: definisi terakhir menang.
	e := tulis(t, map[string]string{
		"l.blade": `@section("slot", "dari layout")@yield("slot")`,
		"c.blade": `@extends("l")@section("slot", "dari child")`,
	})
	out := render(t, e, "c.blade", nil)
	if !strings.Contains(out, "dari child") {
		t.Errorf("definisi child (parse terakhir) harus menang: %q", out)
	}
}
