// Package dominio define as entidades do problema: carona, trecho, reserva e
// participação. Cada empresa guarda as suas caronas, as reservas dos seus
// passageiros (que ela coordena) e as participações: os assentos das suas
// caronas pedidos por reservas, inclusive as coordenadas por outras empresas.
package dominio

import "time"

// Estados de uma reserva (no coordenador) e de uma participação (no dono do trecho)
const (
	Preparando = "preparando" // Reserva ainda coletando os votos dos participantes
	Preparada  = "preparada"  // Participação com assento segurado, aguardando a decisão
	Confirmada = "confirmada"
	Abortada   = "abortada"
	Cancelada  = "cancelada"
)

// Carona é publicada por um motorista.
type Carona struct {
	ID             string
	Motorista      string
	Rota           []string
	Data           string
	PrecoPorTrecho []float64
	AssentosTotais []int
	AssentosLivres []int
	Ativa          bool
}

// NumTrechos devolve quantos trechos esta carona tem.
func (c *Carona) NumTrechos() int {
	return len(c.Rota) - 1
}

// ItemReserva referencia um intervalo de trechos consecutivos de uma carona,
// junto com a empresa dona dela.
type ItemReserva struct {
	Empresa      string
	CaronaID     string
	TrechoInicio int
	TrechoFim    int
	Origem       string
	Destino      string
	Preco        float64
}

// Reserva é o itinerário do passageiro, guardado pela empresa dele, que
// coordena a confirmação em todas as empresas envolvidas.
type Reserva struct {
	ID         string
	Passageiro string
	Itens      []ItemReserva
	Relogio    int64 // Timestamp de Lamport do pedido
	Estado     string
	Pendentes  []string // Empresas que ainda não receberam a decisão
}

// Participacao é a parte de uma reserva que cabe a esta empresa: os assentos
// das suas caronas que foram segurados ou confirmados para aquela reserva.
type Participacao struct {
	ID          string // Mesmo ID da reserva
	Coordenador string // Empresa do passageiro
	Passageiro  string
	Relogio     int64
	Itens       []ItemReserva
	Estado      string
	Expira      time.Time // Prazo para a decisão chegar, enquanto Preparada
}

// PassageiroNoTrecho é usado para responder "quem está reservado nesta carona".
type PassageiroNoTrecho struct {
	Passageiro   string
	TrechoInicio int
	TrechoFim    int
}
