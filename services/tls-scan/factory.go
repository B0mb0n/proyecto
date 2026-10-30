package main

// Checker es el CONTRATO común para cualquier verificación TLS.

type Checker interface {
	Run(target string) string
}

// CertValidityChecker simula validar el certificado.

type CertValidityChecker struct{}

func (CertValidityChecker) Run(target string) string {
	return "Certificado válido"
}

// ProtocolVersionChecker simula revisar qué versiones de TLS soporta.

type ProtocolVersionChecker struct{}

func (ProtocolVersionChecker) Run(target string) string {
	return "TLS 1.2 soportado, TLS 1.3 soportado"
}

func NewChecker(kind string) Checker {
	switch kind {
	case "cert_validity":
		return CertValidityChecker{}
	case "protocol_version":
		return ProtocolVersionChecker{}
	default:
		return nil
	}
}
