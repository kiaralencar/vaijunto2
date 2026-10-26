package main

import (
	"errors"
	"testing"
	"time"

	"vaijunto/internal/dominio"
)

// Menor timestamp de Lamport vence; empate é decidido pelo ID da empresa
func TestCriterioDePrecedencia(t *testing.T) {
	casos := []struct {
		relogioA int64
		empresaA string
		relogioB int64
		empresaB string
		esperado bool
	}{
		{3, "beta", 5, "alfa", true},
		{5, "alfa", 3, "beta", false},
		{4, "alfa", 4, "beta", true},
		{4, "beta", 4, "alfa", false},
	}
	for _, c := range casos {
		if got := temPrecedencia(c.relogioA, c.empresaA, c.relogioB, c.empresaB); got != c.esperado {
			t.Errorf("temPrecedencia(%d,%s,%d,%s) = %v, esperado %v", c.relogioA, c.empresaA, c.relogioB, c.empresaB, got, c.esperado)
		}
	}
}

// Disputa pelo último assento: quem tem precedência recebe como candidata a
// reserva mais nova que está segurando o assento; a mais nova não recebe a mais antiga.
func TestDisputaPeloUltimoAssento(t *testing.T) {
	arm, _ := NovoArmazenamento("")
	e := NovoEstado("beta", arm, &Relogio{})
	c := e.PublicarCarona("motorista", []string{"A", "B"}, "20/11/2026", []float64{10}, []int{1})
	item := []dominio.ItemReserva{{CaronaID: c.ID, TrechoInicio: 0, TrechoFim: 0}}

	// Reserva mais nova (relógio 10) chega primeiro e segura o último assento
	nova := dominio.Participacao{ID: "gama-R1", Coordenador: "gama", Relogio: 10, Itens: item}
	if _, _, err := e.PreparaParticipacao(nova, time.Minute); err != nil {
		t.Fatalf("a primeira reserva deveria segurar o assento: %v", err)
	}

	// Reserva mais antiga (relógio 7) chega atrasada: recebe a mais nova como candidata a ceder
	antiga := dominio.Participacao{ID: "alfa-R1", Coordenador: "alfa", Relogio: 7, Itens: item}
	_, candidatas, err := e.PreparaParticipacao(antiga, time.Minute)
	if !errors.Is(err, errSemAssento) || len(candidatas) != 1 || candidatas[0].ID != "gama-R1" {
		t.Fatalf("a reserva mais antiga deveria ter a mais nova como candidata: err=%v candidatas=%v", err, candidatas)
	}

	// Reserva ainda mais nova (relógio 12) não tem precedência sobre ninguém
	maisNova := dominio.Participacao{ID: "alfa-R2", Coordenador: "alfa", Relogio: 12, Itens: item}
	if _, candidatas, err := e.PreparaParticipacao(maisNova, time.Minute); err == nil || len(candidatas) != 0 {
		t.Fatalf("a reserva mais nova deveria ser recusada sem candidatas: err=%v candidatas=%v", err, candidatas)
	}

	// Depois que a mais nova é abortada pelo coordenador dela, a antiga consegue o assento
	e.AbortaParticipacao("gama-R1")
	if _, _, err := e.PreparaParticipacao(antiga, time.Minute); err != nil {
		t.Fatalf("com o assento liberado a reserva antiga deveria conseguir: %v", err)
	}

	// Uma reserva confirmada não pode mais ser tomada
	e.ConfirmaParticipacao("alfa-R1")
	outra := dominio.Participacao{ID: "gama-R2", Coordenador: "gama", Relogio: 1, Itens: item}
	if _, candidatas, _ := e.PreparaParticipacao(outra, time.Minute); len(candidatas) != 0 {
		t.Fatalf("assento confirmado nao pode ser candidato a ceder: %v", candidatas)
	}
}

// Pedido de preparação que chega depois do aborto é recusado
func TestPreparacaoAtrasadaDepoisDoAborto(t *testing.T) {
	arm, _ := NovoArmazenamento("")
	e := NovoEstado("beta", arm, &Relogio{})
	c := e.PublicarCarona("motorista", []string{"A", "B"}, "20/11/2026", []float64{10}, []int{1})

	e.AbortaParticipacao("alfa-R1")
	p := dominio.Participacao{ID: "alfa-R1", Coordenador: "alfa", Relogio: 1,
		Itens: []dominio.ItemReserva{{CaronaID: c.ID, TrechoInicio: 0, TrechoFim: 0}}}
	if _, _, err := e.PreparaParticipacao(p, time.Minute); !errors.Is(err, errConflito) {
		t.Fatalf("preparacao atrasada deveria ser recusada, err=%v", err)
	}
	if livres := e.ListarCaronas()[0].AssentosLivres[0]; livres != 1 {
		t.Fatalf("o assento nao deveria ter sido segurado, livres=%d", livres)
	}
}
