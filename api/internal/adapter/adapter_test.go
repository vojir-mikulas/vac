package adapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect_Order(t *testing.T) {
	t.Parallel()
	t.Run("compose wins", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "compose.yaml", "services: {}\n")
		write(t, d, "Dockerfile", "FROM alpine\n")
		if got := adapter.Detect(d); got != adapter.KindCompose {
			t.Errorf("got %q, want compose", got)
		}
	})
	t.Run("dockerfile only", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "Dockerfile", "FROM alpine\n")
		if got := adapter.Detect(d); got != adapter.KindDockerfile {
			t.Errorf("got %q, want dockerfile", got)
		}
	})
	t.Run("framework react", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "package.json", `{"dependencies":{"react":"^19.0.0"}}`)
		if got := adapter.Detect(d); got != adapter.KindFramework {
			t.Errorf("got %q, want framework", got)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		t.Parallel()
		if got := adapter.Detect(t.TempDir()); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestDetectFramework_Keys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"next wins over react", map[string]string{"package.json": `{"dependencies":{"next":"15","react":"19"}}`}, adapter.KindFramework},
		{"astro", map[string]string{"package.json": `{"dependencies":{"astro":"5"}}`}, adapter.KindFramework},
		{"vite generic", map[string]string{"package.json": `{"devDependencies":{"vite":"5"}}`}, adapter.KindFramework},
		{"bare node", map[string]string{"package.json": `{"scripts":{"start":"node ."}}`}, adapter.KindFramework},
		{"python requirements", map[string]string{"requirements.txt": "flask\n"}, adapter.KindFramework},
		{"python django", map[string]string{"manage.py": "import django\n"}, adapter.KindFramework},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := t.TempDir()
			for n, b := range c.files {
				write(t, d, n, b)
			}
			if got := adapter.Detect(d); got != c.want {
				t.Errorf("Detect = %q, want %q", got, c.want)
			}
		})
	}
}

// prepareAndRead runs the framework adapter and returns the generated
// Dockerfile.vac + compose.yaml contents.
func prepareAndRead(t *testing.T, d string, cfg adapter.BuildConfig) (df, compose string) {
	t.Helper()
	ad, _ := adapter.For(adapter.KindFramework, d)
	path, err := ad.Prepare(context.Background(), d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	dfB, err := os.ReadFile(filepath.Join(d, "Dockerfile.vac"))
	if err != nil {
		t.Fatal(err)
	}
	cB, _ := os.ReadFile(path)
	return string(dfB), string(cB)
}

func TestFrameworkAdapter_Vite(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	df, compose := prepareAndRead(t, d, adapter.BuildConfig{Framework: "vite"})
	if !strings.Contains(df, "nginx:alpine") {
		t.Errorf("vite should serve static via nginx:\n%s", df)
	}
	if !strings.Contains(compose, `- "80"`) {
		t.Errorf("vite compose should expose 80:\n%s", compose)
	}
	conf, err := os.ReadFile(filepath.Join(d, "vac-nginx.conf"))
	if err != nil || !strings.Contains(string(conf), "/index.html") {
		t.Errorf("vite needs SPA fallback conf: %v\n%s", err, conf)
	}
}

func TestFrameworkAdapter_Next_Modes(t *testing.T) {
	t.Parallel()
	t.Run("standalone", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "next.config.mjs", "export default { output: 'standalone' }\n")
		df, compose := prepareAndRead(t, d, adapter.BuildConfig{Framework: "nextjs"})
		if !strings.Contains(df, `CMD ["node", "server.js"]`) {
			t.Errorf("standalone should run server.js:\n%s", df)
		}
		if !strings.Contains(compose, `- "3000"`) {
			t.Errorf("next compose should expose 3000:\n%s", compose)
		}
	})
	t.Run("export", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "next.config.js", "module.exports = { output: 'export' }\n")
		df, compose := prepareAndRead(t, d, adapter.BuildConfig{Framework: "nextjs"})
		if !strings.Contains(df, "nginx:alpine") || !strings.Contains(df, "/app/out") {
			t.Errorf("export should serve out/ via nginx:\n%s", df)
		}
		if !strings.Contains(compose, `- "80"`) {
			t.Errorf("next export should expose 80:\n%s", compose)
		}
	})
	t.Run("default", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "nextjs"})
		if !strings.Contains(df, "next") || !strings.Contains(df, "start") {
			t.Errorf("default should run next start:\n%s", df)
		}
	})
}

func TestFrameworkAdapter_Astro(t *testing.T) {
	t.Parallel()
	t.Run("static MPA", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "package.json", `{"dependencies":{"astro":"5"}}`)
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "astro"})
		if !strings.Contains(df, "nginx:alpine") {
			t.Errorf("astro static should use nginx:\n%s", df)
		}
		conf, _ := os.ReadFile(filepath.Join(d, "vac-nginx.conf"))
		if strings.Contains(string(conf), "/index.html") {
			t.Errorf("astro is MPA, must not SPA-fallback to index.html:\n%s", conf)
		}
	})
	t.Run("ssr standalone", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "package.json", `{"dependencies":{"astro":"5","@astrojs/node":"9"}}`)
		df, compose := prepareAndRead(t, d, adapter.BuildConfig{Framework: "astro"})
		if !strings.Contains(df, "dist/server/entry.mjs") {
			t.Errorf("astro ssr should run entry.mjs:\n%s", df)
		}
		if !strings.Contains(compose, `- "4321"`) {
			t.Errorf("astro ssr should expose 4321:\n%s", compose)
		}
	})
}

func TestFrameworkAdapter_Node(t *testing.T) {
	t.Parallel()
	t.Run("start script", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "package.json", `{"scripts":{"start":"node ."}}`)
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "node"})
		if !strings.Contains(df, `CMD ["dumb-init", "npm", "start"]`) {
			t.Errorf("node with start script should npm start:\n%s", df)
		}
	})
	t.Run("entry file fallback", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "package.json", `{}`)
		write(t, d, "index.js", "console.log(1)\n")
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "node", Port: 8080})
		if !strings.Contains(df, `CMD ["dumb-init", "node", "index.js"]`) {
			t.Errorf("node should fall back to index.js:\n%s", df)
		}
		if !strings.Contains(df, "ENV PORT=8080") {
			t.Errorf("node should honor configured port:\n%s", df)
		}
	})
}

func TestFrameworkAdapter_Python(t *testing.T) {
	t.Parallel()
	t.Run("django", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "requirements.txt", "django\n")
		write(t, d, "manage.py", "import django\n")
		write(t, d, "myproj/wsgi.py", "application = None\n")
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "python"})
		if !strings.Contains(df, "myproj.wsgi:application") || !strings.Contains(df, "gunicorn") {
			t.Errorf("django should gunicorn the wsgi app:\n%s", df)
		}
	})
	t.Run("fastapi", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "requirements.txt", "fastapi\n")
		write(t, d, "main.py", "from fastapi import FastAPI\napp = FastAPI()\n")
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "python"})
		if !strings.Contains(df, "uvicorn") || !strings.Contains(df, "main:app") {
			t.Errorf("fastapi should run uvicorn:\n%s", df)
		}
	})
	t.Run("procfile override", func(t *testing.T) {
		t.Parallel()
		d := t.TempDir()
		write(t, d, "requirements.txt", "flask\n")
		write(t, d, "Procfile", "web: gunicorn wsgi:app --workers 3\n")
		df, _ := prepareAndRead(t, d, adapter.BuildConfig{Framework: "python"})
		if !strings.Contains(df, "CMD gunicorn wsgi:app --workers 3") {
			t.Errorf("python should honor Procfile web line:\n%s", df)
		}
	})
}

func TestFor_AutoResolvesAndUndetected(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "Dockerfile", "FROM alpine\n")
	ad, err := adapter.For(adapter.KindAuto, d)
	if err != nil {
		t.Fatal(err)
	}
	if ad.Kind() != adapter.KindDockerfile {
		t.Errorf("kind = %q, want dockerfile", ad.Kind())
	}

	if _, err := adapter.For(adapter.KindAuto, t.TempDir()); err == nil {
		t.Error("expected ErrUndetected for empty repo")
	}
}

func TestComposeAdapter_Prepare(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "compose.yml", "services: {}\n")
	ad, _ := adapter.For(adapter.KindCompose, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(d, "compose.yml") {
		t.Errorf("path = %s", path)
	}
}

func TestDockerfileAdapter_Prepare_CustomPath(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "docker/Api.Dockerfile", "FROM alpine\n")
	ad, _ := adapter.For(adapter.KindDockerfile, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{DockerfilePath: "docker/Api.Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "docker/Api.Dockerfile") {
		t.Errorf("generated compose missing dockerfile path:\n%s", body)
	}
	if !strings.Contains(string(body), "context: .") {
		t.Errorf("generated compose missing build context:\n%s", body)
	}
}

func TestDockerfileAdapter_Prepare_Missing(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindDockerfile, d)
	if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{DockerfilePath: "nope.Dockerfile"}); err == nil {
		t.Error("expected error for missing Dockerfile")
	}
}

func TestStaticAdapter_Prepare_SPAFallback(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "public/index.html", "<html></html>")
	ad, _ := adapter.For(adapter.KindStatic, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{StaticDir: "public", SPAFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(path)
	if !strings.Contains(string(compose), "nginx:alpine") || !strings.Contains(string(compose), "./public:/usr/share/nginx/html") {
		t.Errorf("compose missing nginx/static mount:\n%s", compose)
	}
	conf, err := os.ReadFile(filepath.Join(d, "vac-static.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "/index.html") {
		t.Errorf("spa fallback conf missing index.html:\n%s", conf)
	}
}

func TestStaticAdapter_Prepare_RejectsUnsafeDir(t *testing.T) {
	t.Parallel()
	// A StaticDir with a ':' could inject extra fields into the generated
	// compose volume line; reject it before it reaches the template.
	for _, dir := range []string{"pub:lic", "pub\"lic", "pub\nlic"} {
		d := t.TempDir()
		ad, _ := adapter.For(adapter.KindStatic, d)
		if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{StaticDir: dir}); err == nil {
			t.Errorf("StaticDir %q should be rejected", dir)
		}
	}
}

func TestStaticAdapter_Prepare_No404Fallback(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	write(t, d, "index.html", "<html></html>")
	ad, _ := adapter.For(adapter.KindStatic, d)
	if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{}); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(d, "vac-static.conf"))
	if !strings.Contains(string(conf), "=404") {
		t.Errorf("non-spa conf should 404 unknown paths:\n%s", conf)
	}
}

func TestFrameworkAdapter_Prepare_React(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindFramework, d)
	path, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{Framework: "React", BuildCommand: "pnpm i && pnpm build"})
	if err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(path)
	if !strings.Contains(string(compose), "Dockerfile.vac") {
		t.Errorf("compose missing generated dockerfile ref:\n%s", compose)
	}
	df, err := os.ReadFile(filepath.Join(d, "Dockerfile.vac"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "pnpm i && pnpm build") || !strings.Contains(string(df), "nginx:alpine") {
		t.Errorf("generated Dockerfile wrong:\n%s", df)
	}
}

func TestFrameworkAdapter_Prepare_Unsupported(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	ad, _ := adapter.For(adapter.KindFramework, d)
	if _, err := ad.Prepare(context.Background(), d, adapter.BuildConfig{Framework: "svelte"}); err == nil {
		t.Error("expected unsupported-framework error")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	if err := adapter.Validate(adapter.KindFramework, adapter.BuildConfig{}); err == nil {
		t.Error("framework without framework field should fail validation")
	}
	if err := adapter.Validate(adapter.KindFramework, adapter.BuildConfig{Framework: "react"}); err != nil {
		t.Errorf("valid framework config rejected: %v", err)
	}
	if err := adapter.Validate("bogus", adapter.BuildConfig{}); err == nil {
		t.Error("unknown kind should fail validation")
	}
}

func TestParseConfig_RoundTrip(t *testing.T) {
	t.Parallel()
	cfg, err := adapter.ParseConfig(json.RawMessage(`{"composePath":"deploy/stack.yaml","port":3000}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposePath != "deploy/stack.yaml" || cfg.Port != 3000 {
		t.Errorf("round-trip mismatch: %+v", cfg)
	}
	if _, err := adapter.ParseConfig(nil); err != nil {
		t.Errorf("nil config should parse to zero value: %v", err)
	}
}
