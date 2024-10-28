package main

import (
	"bytes"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"log"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-smtp"
)

func main() {
	s := smtp.NewServer(&Backend{})

	s.Addr = ":2525"
	s.Domain = "localhost"
	s.WriteTimeout = 10 * time.Second
	s.ReadTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true

	log.Println("Starting server at", s.Addr)
	if err := s.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// Backend реализация SMTP сервера
type Backend struct{}

func (bkd *Backend) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &Session{}, nil
}

// Session объект создается после команды EHLO
type Session struct{}

// Теперь реализуем методы Session
func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	fmt.Println("Mail from:", from)
	s.From = from
	return nil
}

func (s *Session) Rcpt(to string) error {
	fmt.Println("Rcpt to:", to)
	s.To = append(s.To, to)
	return nil
}

func (s *Session) Data(r io.Reader) error {
	if data, err := io.ReadAll(r); err != nil {
		return err
	} else {
		fmt.Println("Received message:", string(data))
    for _, recipient := range s.To {
      if err := sendMail(s.From, recipient, data); err != nil {
        fmt.Printf("Failed to send email to %s: %v", recipient, err)
      } else {
        fmt.Printf("Email sent successfully to %s", recipient)
      }
    }

		return nil
	}
}

func (s *Session) AuthPlain(username, password string) error {
	if username != "testuser" || password != "testpass" {
		return fmt.Errorf("Invalid username or password")
	}

	return nil
}

func (s *Session) Logout() error {
	return nil
}

func lookupMX(domain string) ([]*net.MX, error) {
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return nil, fmt.Errorf("Error looking up MX records: %v", err)
	}

	return mxRecords, nil
}

func sendMail(from string, to string, data []byte) error {
  domain := strings.Split(to, "@")[1]

  mxRecords, err := lookupMX(domain)
  if err != nil {
    return err
  }

  for _, mx := range mxRecords {
    host := mx.Host

    for _, port := range []int{25, 587, 465} {
      address := fmt.Sprintf("%s:%d", host, port)

      var c *smtp.Client

      var err error

      switch port {
      case 465:
        //SMTP
        tlsConfig := &tls.Config{ServerName: host}
        conn, err := tls.Dial("tcp", address, tlsConfig)
        if err != nil {
          continue
        }

        c, err = smtp.NewClient(conn, host)

      case 25, 587:
        // SMTP или SMTP с STARTTLS
        c, err = smtp.Dial(address)
        if err != nil {
          continue
        }

        if port = 587 {
          if err = c.StartTLS(&tls.Config{ServerName: host}); err != nil {
            c.Close()
            continue
          }
        }
      }

      if err != nil {
        continue
      }

      // Подписание сообщения DKIM подписью
      var b bytes.Buffer
      if err := dkim.Sign(&b, bytes.NewReader(data), dkimOptions); err != nil {
        return fmt.Errorf("Failed to sign email with DKIM: %v", err)
      }
      signedData := b.Bytes()

      // SMTP взаимодействие
      if err = c.Mail(from); err != nil {
        c.Close()
        continue
      }

      if err = c.Rcpt(to); err != nil {
        c.Close()
        continue
      }

      w, err := c.Data()

      if err != nil {
        c.Close()
        continue
      }

      _, err = w.Write(signedData) // Используем сообщение, подписанное DKIM
      if err != nil {
        c.Close()
        continue
      }
      err = w.Close()
      if err != nil {
        c.Close()
        continue
      }
      c.Quit()
      return nil
    }
  }

  return fmt.Errorf("Failed to send email to %s", to)
}

// Загружаем приватный DKIM ключ
var dkimPrivateKey *rsa.PrivateKey

func init() {
  // Загружаем приватный DKIM ключ из файла
  privateKeyPEM, err := os.ReadFile("path/to/your/private_key.pem") // заменить на реальный путь к вашему закрытому DKIM ключу
  if err != nil {
    log.Fatalf("Failed to read private key: %v", err)
  }

  block, _ := pem.Decode(privateKeyPEM)
  if block == nil {
    log.Fatalf("Failed to parse PEM block containing the private key")
  }

  privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
  if err != nil {
    log.Fatalf("Failed to parse private key: %v", err)
  }

  dkimPrivateKey = privateKey
}

// DKIM опции; обновить Domain и Selector, чтобы они соответствовали вашей DNS записи для DKIM.
var dkimOptions = &dkim.SignOptions{
  Domain: "example.com",
  Selector: "default",
  Signer: dkimPrivateKey,
}
