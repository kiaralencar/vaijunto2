package main

import (
	"net/http"
	"strconv"
	"sync"
)

// Cabeçalho HTTP que carrega o relógio de Lamport em toda mensagem entre servidores
const cabecalhoLamport = "X-Relogio-Lamport"

// Relogio é o relógio lógico de Lamport desta empresa
type Relogio struct {
	mu    sync.Mutex
	valor int64
}

// Avança o relógio para um evento local e devolve o novo valor
func (r *Relogio) Tick() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.valor++
	return r.valor
}

// Ajusta o relógio ao receber uma mensagem: max(local, recebido) + 1
func (r *Relogio) Recebe(recebido int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if recebido > r.valor {
		r.valor = recebido
	}
	r.valor++
	return r.valor
}

// Define o valor (usado ao carregar o estado do disco)
func (r *Relogio) Define(v int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v > r.valor {
		r.valor = v
	}
}

// Lê o relógio do cabeçalho, se existir, e atualiza o relógio local
func (r *Relogio) LeCabecalho(h http.Header) {
	if v, err := strconv.ParseInt(h.Get(cabecalhoLamport), 10, 64); err == nil {
		r.Recebe(v)
	}
}

// Middleware: atualiza o relógio em toda requisição recebida e o envia na resposta
func (r *Relogio) Middleware(proximo http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.LeCabecalho(req.Header)
		w.Header().Set(cabecalhoLamport, strconv.FormatInt(r.Tick(), 10))
		proximo.ServeHTTP(w, req)
	})
}

// Indica se a reserva (relogioA, empresaA) tem precedência sobre (relogioB, empresaB):
// menor timestamp de Lamport vence, e o empate é decidido pelo ID da empresa
func temPrecedencia(relogioA int64, empresaA string, relogioB int64, empresaB string) bool {
	if relogioA != relogioB {
		return relogioA < relogioB
	}
	return empresaA < empresaB
}
