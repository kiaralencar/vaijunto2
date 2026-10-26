package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"vaijunto/internal/dominio"
)

// Resposta do GET /status
type RespostaStatus struct {
	Empresa string `json:"empresa"`
	Status  string `json:"status"`
}

// Carona como é enviada às outras empresas (sem o motorista)
type CaronaREST struct {
	ID             string    `json:"id"`
	Rota           []string  `json:"rota"`
	Data           string    `json:"data"`
	PrecoPorTrecho []float64 `json:"preco_por_trecho"`
	AssentosLivres []int     `json:"assentos_livres"`
}

// Resposta do GET /caronas
type RespostaCaronas struct {
	Empresa string       `json:"empresa"`
	Caronas []CaronaREST `json:"caronas"`
}

// Item de reserva trocado entre servidores
type ItemREST struct {
	Empresa      string  `json:"empresa"`
	CaronaID     string  `json:"carona_id"`
	TrechoInicio int     `json:"trecho_inicio"`
	TrechoFim    int     `json:"trecho_fim"`
	Origem       string  `json:"origem,omitempty"`
	Destino      string  `json:"destino,omitempty"`
	Preco        float64 `json:"preco,omitempty"`
}

// Corpo do PUT /participacoes/{id}
type PedidoPreparacao struct {
	Coordenador string     `json:"coordenador"`
	Passageiro  string     `json:"passageiro"`
	Relogio     int64      `json:"relogio"`
	Itens       []ItemREST `json:"itens"`
}

// Resposta do PUT /participacoes/{id}
type RespostaPreparacao struct {
	Reserva string     `json:"reserva,omitempty"`
	Estado  string     `json:"estado,omitempty"`
	Itens   []ItemREST `json:"itens,omitempty"`
	Erro    string     `json:"erro,omitempty"`
}

// Resposta das operações que só informam a situação de uma reserva
type RespostaSituacao struct {
	Reserva string `json:"reserva,omitempty"`
	Estado  string `json:"estado,omitempty"`
	Erro    string `json:"erro,omitempty"`
}

// Resposta de erro
type RespostaErro struct {
	Erro string `json:"erro"`
}

// Sobe a API REST usada pelos outros servidores do consórcio
func (s *Servidor) IniciaAPI() {
	mux := http.NewServeMux()

	// Informa o ID da empresa e que o servidor está ativo
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		escreveJSON(w, http.StatusOK, RespostaStatus{Empresa: s.cfg.Empresa, Status: "ativo"})
	})

	// Lista as caronas ativas desta empresa, opcionalmente só de uma data (?data=DD/MM/AAAA)
	mux.HandleFunc("GET /caronas", func(w http.ResponseWriter, r *http.Request) {
		data := r.URL.Query().Get("data")
		caronas := s.estado.ListarCaronas()
		lista := make([]CaronaREST, 0, len(caronas))
		for _, c := range caronas {
			if data != "" && c.Data != data {
				continue
			}
			lista = append(lista, CaronaREST{
				ID:             c.ID,
				Rota:           c.Rota,
				Data:           c.Data,
				PrecoPorTrecho: c.PrecoPorTrecho,
				AssentosLivres: c.AssentosLivres,
			})
		}
		escreveJSON(w, http.StatusOK, RespostaCaronas{Empresa: s.cfg.Empresa, Caronas: lista})
	})

	// Fase 1 (participante): segura os assentos desta empresa para a reserva
	mux.HandleFunc("PUT /participacoes/{id}", func(w http.ResponseWriter, r *http.Request) {
		var pedido PedidoPreparacao
		if err := json.NewDecoder(r.Body).Decode(&pedido); err != nil || pedido.Coordenador == "" || len(pedido.Itens) == 0 {
			escreveJSON(w, http.StatusBadRequest, RespostaErro{Erro: "corpo invalido"})
			return
		}
		p := dominio.Participacao{
			ID:          r.PathValue("id"),
			Coordenador: pedido.Coordenador,
			Passageiro:  pedido.Passageiro,
			Relogio:     pedido.Relogio,
			Itens:       deItensREST(pedido.Itens),
		}
		itens, err := s.preparaLocal(p)
		if err != nil {
			escreveErro(w, err)
			return
		}
		escreveJSON(w, http.StatusOK, RespostaPreparacao{Reserva: p.ID, Estado: dominio.Preparada, Itens: paraItensREST(itens)})
	})

	// Fase 2 (participante): confirma, aborta ou cancela a participação
	mux.HandleFunc("POST /participacoes/{id}/confirmar", s.acaoParticipacao(s.estado.ConfirmaParticipacao, dominio.Confirmada))
	mux.HandleFunc("POST /participacoes/{id}/abortar", s.acaoParticipacao(s.estado.AbortaParticipacao, dominio.Abortada))
	mux.HandleFunc("POST /participacoes/{id}/cancelar", s.acaoParticipacao(s.estado.CancelaParticipacao, dominio.Cancelada))

	// Coordenador: informa a decisão sobre uma reserva (participante com prazo vencido)
	mux.HandleFunc("GET /reservas/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		situacao, conhecida := s.estado.SituacaoReserva(id)
		if !conhecida {
			escreveJSON(w, http.StatusNotFound, RespostaSituacao{Reserva: id, Erro: "reserva desconhecida"})
			return
		}
		escreveJSON(w, http.StatusOK, RespostaSituacao{Reserva: id, Estado: situacao})
	})

	// Coordenador: outra reserva com precedência pede que esta seja abortada
	mux.HandleFunc("POST /reservas/{id}/preempcao", s.acaoReserva(s.estado.Preempcao))

	// Coordenador: uma carona da reserva foi cancelada pelo motorista
	mux.HandleFunc("POST /reservas/{id}/invalidacao", s.acaoReserva(s.estado.InvalidaReserva))

	// Fotografia do estado, usada pelo teste com falhas
	mux.HandleFunc("GET /auditoria", func(w http.ResponseWriter, r *http.Request) {
		escreveJSON(w, http.StatusOK, s.estado.Auditoria())
	})

	if err := http.ListenAndServe(":"+s.cfg.PortaHTTP, s.relogio.Middleware(mux)); err != nil {
		fmt.Println("Erro na API REST:", err)
		os.Exit(1)
	}
}

// Monta o handler de confirmar/abortar/cancelar uma participação
func (s *Servidor) acaoParticipacao(acao func(id string) error, estadoFinal string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := acao(id); err != nil {
			escreveErro(w, err)
			return
		}
		escreveJSON(w, http.StatusOK, RespostaSituacao{Reserva: id, Estado: estadoFinal})
	}
}

// Monta o handler de preempção/invalidação de uma reserva coordenada por esta empresa
func (s *Servidor) acaoReserva(acao func(id string) (dominio.Reserva, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		reserva, err := acao(id)
		if err != nil {
			escreveErro(w, err)
			return
		}
		s.propaga(reserva)
		escreveJSON(w, http.StatusOK, RespostaSituacao{Reserva: id, Estado: reserva.Estado})
	}
}

// Monta a resposta HTTP em JSON com o código de status dado
func escreveJSON(w http.ResponseWriter, codigo int, dados interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(codigo)
	json.NewEncoder(w).Encode(dados)
}

// Escolhe o código HTTP de acordo com o erro
func escreveErro(w http.ResponseWriter, err error) {
	codigo := http.StatusInternalServerError
	switch {
	case errors.Is(err, errInvalido):
		codigo = http.StatusBadRequest
	case errors.Is(err, errNaoEncontrada):
		codigo = http.StatusNotFound
	case errors.Is(err, errSemAssento), errors.Is(err, errConflito):
		codigo = http.StatusConflict
	}
	escreveJSON(w, codigo, RespostaErro{Erro: err.Error()})
}

// Converte a participação para o corpo do PUT /participacoes/{id}
func paraPedidoPreparacao(p dominio.Participacao) PedidoPreparacao {
	return PedidoPreparacao{
		Coordenador: p.Coordenador,
		Passageiro:  p.Passageiro,
		Relogio:     p.Relogio,
		Itens:       paraItensREST(p.Itens),
	}
}

func paraItensREST(itens []dominio.ItemReserva) []ItemREST {
	lista := make([]ItemREST, len(itens))
	for i, it := range itens {
		lista[i] = ItemREST(it)
	}
	return lista
}

func deItensREST(itens []ItemREST) []dominio.ItemReserva {
	lista := make([]dominio.ItemReserva, len(itens))
	for i, it := range itens {
		lista[i] = dominio.ItemReserva(it)
	}
	return lista
}
