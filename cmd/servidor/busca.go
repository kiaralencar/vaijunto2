package main

import (
	"net/http"
	"net/url"
	"sort"
	"sync"

	"vaijunto/internal/dominio"
)

// Aresta representa um trecho com assento livre, no grafo de busca. De/para
// são cidades (os nós). Cada aresta sabe a que carona e empresa pertence, para
// que o caminho encontrado possa virar um pedido de reserva de verdade.
type aresta struct {
	empresa  string
	caronaID string
	trecho   int
	de, para string
	preco    float64
}

// itinerarioEncontrado é o resultado de um caminho no grafo: uma lista de
// itens de reserva e o preço total somado.
type itinerarioEncontrado struct {
	itens []dominio.ItemReserva
	preco float64
}

// caronaConsorcio é uma carona de qualquer empresa, como vista por esta
type caronaConsorcio struct {
	Empresa string
	dominio.Carona
}

// Limites de segurança para a busca não "explodir"
const (
	profundidadeMaximaBusca = 6  // No maximo 6 trechos por itinerario
	maxItinerarios          = 10 // Para de procurar depois de achar 10 caminhos
)

// Junta as caronas desta empresa com as dos vizinhos, consultados em paralelo.
// Vizinho fora do ar é ignorado: a busca continua com as empresas disponíveis.
func (s *Servidor) caronasDoConsorcio(data string) []caronaConsorcio {
	var lista []caronaConsorcio
	for _, c := range s.estado.ListarCaronas() {
		if data == "" || c.Data == data {
			lista = append(lista, caronaConsorcio{Empresa: s.cfg.Empresa, Carona: c})
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, endereco := range s.vizinhos.Enderecos() {
		wg.Add(1)
		go func(endereco string) {
			defer wg.Done()
			var resp RespostaCaronas
			caminho := endereco + "/caronas"
			if data != "" {
				caminho += "?data=" + url.QueryEscape(data)
			}
			status, err := s.requisicaoURL(http.MethodGet, caminho, nil, &resp)
			if err != nil || status != http.StatusOK || resp.Empresa == s.cfg.Empresa {
				return
			}
			s.vizinhos.registra(endereco, resp.Empresa)
			mu.Lock()
			defer mu.Unlock()
			for _, c := range resp.Caronas {
				lista = append(lista, caronaConsorcio{Empresa: resp.Empresa, Carona: dominio.Carona{
					ID:             c.ID,
					Rota:           c.Rota,
					Data:           c.Data,
					PrecoPorTrecho: c.PrecoPorTrecho,
					AssentosLivres: c.AssentosLivres,
					Ativa:          true,
				}})
			}
		}(endereco)
	}
	wg.Wait()
	return lista
}

// BuscarItinerarios monta o grafo a partir das caronas do consórcio naquela data,
// e faz uma DFS de origem a destino.
func BuscarItinerarios(caronas []caronaConsorcio, origem, destino string) []itinerarioEncontrado {
	// Ordem fixa das caronas, para a busca dar o mesmo resultado independente da ordem de chegada
	sort.Slice(caronas, func(i, j int) bool { return caronas[i].ID < caronas[j].ID })

	// Monta o grafo: cada cidade é um nó, cada trecho com assento livre é uma aresta.
	grafo := make(map[string][]aresta)
	for _, c := range caronas {
		for t := 0; t < c.NumTrechos(); t++ {
			if c.AssentosLivres[t] <= 0 {
				continue // Sem assento neste trecho: nao vira aresta
			}
			de, para := c.Rota[t], c.Rota[t+1]
			grafo[de] = append(grafo[de], aresta{
				empresa:  c.Empresa,
				caronaID: c.ID,
				trecho:   t,
				de:       de,
				para:     para,
				preco:    c.PrecoPorTrecho[t],
			})
		}
	}

	// DFS (Depth First Search) para achar caminhos de origem a destino, respeitando
	// os limites de profundidade e quantidade de itinerarios encontrados.
	var resultados []itinerarioEncontrado
	visitado := map[string]bool{origem: true}
	var caminho []aresta

	var dfs func(atual string)
	dfs = func(atual string) {
		if len(resultados) >= maxItinerarios {
			return // Itinerários demais? Para de procurar.
		}
		if atual == destino && len(caminho) > 0 {
			resultados = append(resultados, montaItinerario(caminho))
			return // Achou o destino? Para de procurar.
		}
		if len(caminho) >= profundidadeMaximaBusca {
			return // Já foi fundo demais? Para de procurar.
		}
		for _, a := range grafo[atual] {
			if visitado[a.para] {
				continue // Nao revisita cidade: evita ciclo infinito
			}
			visitado[a.para] = true
			caminho = append(caminho, a)

			dfs(a.para)

			// Desfaz a escolha para tentar o proximo caminho (backtracking)
			caminho = caminho[:len(caminho)-1]
			visitado[a.para] = false
		}
	}
	dfs(origem)

	// Critério de ordenação: mais barato primeiro. Em caso de empate no preço,
	// o itinerário com menos itens (menos baldeações entre motoristas) vem primeiro.
	sort.SliceStable(resultados, func(i, j int) bool {
		if resultados[i].preco != resultados[j].preco {
			return resultados[i].preco < resultados[j].preco
		}
		return len(resultados[i].itens) < len(resultados[j].itens)
	})

	return resultados
}

// montaItinerario junta arestas consecutivas da mesma carona num único ItemReserva
func montaItinerario(caminho []aresta) itinerarioEncontrado {
	var itens []dominio.ItemReserva
	var preco float64

	for _, a := range caminho {
		preco += a.preco
		n := len(itens)
		if n > 0 && itens[n-1].CaronaID == a.caronaID && itens[n-1].TrechoFim == a.trecho-1 {
			itens[n-1].TrechoFim = a.trecho
			itens[n-1].Destino = a.para
			itens[n-1].Preco += a.preco
			continue
		}
		itens = append(itens, dominio.ItemReserva{
			Empresa:      a.empresa,
			CaronaID:     a.caronaID,
			TrechoInicio: a.trecho,
			TrechoFim:    a.trecho,
			Origem:       a.de,
			Destino:      a.para,
			Preco:        a.preco,
		})
	}

	return itinerarioEncontrado{itens: itens, preco: preco}
}
