package preview

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
	"github.com/titanous/json5"
	"github.com/xmit-co/xmit/config"
)

type handler struct {
	directory string
}

func openFile(path string) *os.File {
	s, err := os.Stat(path)
	if err == nil && !s.IsDir() {
		f, _ := os.Open(path)
		return f
	}
	return nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := config.XmitConfig{}
	jsonPath := filepath.Join(h.directory, "xmit.json")
	tomlPath := filepath.Join(h.directory, "xmit.toml")
	cfgBytes, err := os.ReadFile(jsonPath)
	if err == nil {
		err = json5.Unmarshal(cfgBytes, &cfg)
		if err != nil {
			log.Printf("⚠️ %s: %v", jsonPath, err)
		}
	} else if os.IsNotExist(err) {
		cfgBytes, err = os.ReadFile(tomlPath)
		if err == nil {
			err = toml.Unmarshal(cfgBytes, &cfg)
		}
		if err != nil && !os.IsNotExist(err) {
			log.Printf("⚠️ %s: %v", tomlPath, err)
		}
	} else {
		log.Printf("⚠️ %s: %v", jsonPath, err)
	}
	w.Header().Add("Server", "xmit")
	w.Header().Add("X-Frame-Options", "SAMEORIGIN")
	w.Header().Add("X-Content-Type-Options", "nosniff")
	w.Header().Add("Referrer-Policy", "no-referrer")
	w.Header().Add("Accept-Ranges", "bytes")
	for _, header := range cfg.Headers {
		apply := false
		if header.On == nil {
			apply = true
		} else {
			re, err := regexp.Compile(*header.On)
			if err != nil {
				continue
			}
			apply = re.MatchString(r.URL.Path)
		}
		if apply {
			if header.Value == nil {
				w.Header().Del(header.Name)
			} else {
				w.Header().Set(header.Name, *header.Value)
			}
		}
	}
	if r.Method == http.MethodPost {
		matched := false
		for _, form := range cfg.Forms {
			if form.From == r.URL.Path {
				matched = true
				if err := sendFormByMail(r, form.To); err != nil {
					internalError(w, err)
					return
				}
				if form.Then != "" {
					http.Redirect(w, r, form.Then, http.StatusFound)
					return
				}
			}
		}
		if !matched {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}

	status := http.StatusOK
	p := strings.Trim(r.URL.Path, "/")
	op := filepath.Join(h.directory, p)
	realp := op
	f := openFile(realp)
	if f == nil {
		realp = filepath.Join(op, "index.html")
		f = openFile(realp)
	}
	if f == nil {
		realp = op + ".html"
		f = openFile(realp)
	}
	if f == nil {
		for _, redirect := range cfg.Redirects {
			from, err := regexp.Compile(redirect.From)
			if err != nil {
				continue
			}
			if from.MatchString(r.URL.Path) {
				to := from.ReplaceAllString(r.URL.Path, redirect.To)
				code := http.StatusTemporaryRedirect
				if redirect.Permanent {
					code = http.StatusMovedPermanently
				}
				http.Redirect(w, r, to, code)
				return
			}
		}
	}
	if f == nil && cfg.Fallback != "" {
		realp = filepath.Join(h.directory, cfg.Fallback)
		f = openFile(realp)
	}
	if f == nil {
		status = http.StatusNotFound
		if cfg.FourOFour != "" {
			realp = filepath.Join(h.directory, cfg.FourOFour)
			f = openFile(realp)
		}
	}
	if f == nil {
		http.NotFound(w, r)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			log.Printf("Error closing file: %v", err)
		}
	}(f)

	http.ServeContent(w, r, realp, time.Now(), f)
}

func sendFormByMail(r *http.Request, to string) error {
	mediaTypeHeader := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(mediaTypeHeader)
	if mediaType == "multipart/form-data" {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			return err
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return err
		}
	}
	replyTo := r.Form.Get("email")
	_, err := mail.ParseAddress(replyTo)
	var from string
	if err != nil {
		replyTo = "noreply@forms.xmit.co"
		from = "noreply@forms.xmit.co"
	} else {
		from = strings.Replace(replyTo, "@", ".", 1) + "@forms.xmit.co"
	}
	fromName := r.Form.Get("name")
	if fromName == "" {
		fromName = r.Host
	}
	subject := r.Form.Get("subject")
	if subject == "" {
		subject = "Form submission"
	}
	subject = fmt.Sprintf("[%s] %s", r.Host, subject)

	var body strings.Builder
	header := make(map[string]interface{})
	for k, v := range r.Form {
		if k == "email" || k == "name" || k == "subject" || k == "message" || (r.MultipartForm != nil && r.MultipartForm.File[k] != nil) {
			continue
		}
		if len(v) == 1 {
			header[k] = v[0]
		} else {
			header[k] = v
		}
	}
	if len(header) > 0 {
		body.WriteString("---\n")
		if err := toml.NewEncoder(&body).Encode(header); err != nil {
			return err
		}
		body.WriteString("---\n")
	}
	body.WriteString(r.Form.Get("message"))

	log.Printf("From: %s", fmt.Sprintf("%s <%s>", fromName, from))
	log.Printf("Reply-To: %s", fmt.Sprintf("%s <%s>", fromName, replyTo))
	log.Printf("To: %s", to)
	log.Printf("Subject: %s", subject)

	if r.MultipartForm != nil {
		for prefix, headers := range r.MultipartForm.File {
			for _, header := range headers {
				file, err := header.Open()
				if err != nil {
					return err
				}
				content, err := io.ReadAll(file)
				_ = file.Close()
				if err != nil {
					return err
				}
				log.Printf("Attachment: %s_%s (%d bytes)", prefix, header.Filename, len(content))
			}
		}
	}
	log.Printf(body.String())
	return nil
}

func Serve(directory string) error {
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":4000"
		log.Printf("Listening on %s (set LISTEN to override)", listen)
	} else {
		log.Printf("Listening on %s", listen)
	}
	serveAddr := listen
	if serveAddr[0] == ':' {
		serveAddr = "localhost" + serveAddr
	}
	log.Printf("Preview of %s: http://%s", directory, serveAddr)
	return http.ListenAndServe(listen, &handler{directory})
}

func internalError(w http.ResponseWriter, err error) {
	u := uuid.New()
	log.Printf("%s: %v", u.String(), err)
	http.Error(w, fmt.Sprintf("Internal error (%v)", u), http.StatusInternalServerError)
}
