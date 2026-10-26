// Simulador de passageiros, sem interface gráfica. Conecta vários passageiros
// ao mesmo tempo nos servidores de todas as empresas do consórcio e gera, de
// forma autônoma, buscas, reservas e cancelamentos durante o tempo pedido.
// Durante a simulação mostra o andamento a intervalos regulares, e no final
// mostra quantas operações foram feitas e os tempos de resposta.
// Com -semear, antes publica caronas em cadeia entre as empresas, para que
// existam itinerários que só se completam combinando trechos de várias delas.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"vaijunto/internal/cliente"
	"vaijunto/internal/protocolo"
)

// Cidades da cadeia usada na semeadura: a empresa i liga a cidade i à cidade i+1
var cidades = []string{"Salvador", "Feira de Santana", "Vitoria da Conquista", "Montes Claros",
	"Janauba", "Monte Azul", "Guanambi", "Brumado"}

// Metricas acumula os resultados de todos os passageiros
type Metricas struct {
	mu        sync.Mutex
	contagem  map[string]int
	latencias map[string][]time.Duration
}

func (m *Metricas) registra(operacao, resultado string, duracao time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contagem[operacao+" "+resultado]++
	if duracao > 0 {
		m.latencias[operacao] = append(m.latencias[operacao], duracao)
	}
}

// Soma as contagens cujo nome começa com o prefixo dado
func (m *Metricas) soma(prefixo string) int {
	total := 0
	for k, v := range m.contagem {
		if strings.HasPrefix(k, prefixo) {
			total += v
		}
	}
	return total
}

// Mostra o andamento da simulação a cada intervalo, até o canal "parar" ser fechado
func (m *Metricas) mostraProgresso(inicio time.Time, duracao, intervalo time.Duration, parar chan struct{}) {
	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()
	for {
		select {
		case <-parar:
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		fmt.Printf("[%3.0fs/%.0fs] buscas: %d (sem itinerario: %d) | reservas confirmadas: %d | recusadas: %d | cancelamentos: %d | quedas de conexao: %d\n",
			time.Since(inicio).Seconds(), duracao.Seconds(),
			m.soma("busca "), m.soma("busca sem itinerario"),
			m.soma("reserva confirmada"), m.soma("reserva recusada"),
			m.soma("cancelamento ok"), m.soma("busca conexao")+m.soma("reserva conexao")+m.soma("cancelamento conexao")+m.soma("conexao falhou"))
		m.mu.Unlock()
	}
}

func main() {
	servidores := flag.String("servidores", "127.0.0.1:8080,127.0.0.1:8081,127.0.0.1:8082", "enderecos TCP dos servidores das empresas, separados por virgula")
	passageiros := flag.Int("passageiros", 100, "numero de passageiros simultaneos")
	duracao := flag.Duration("duracao", 30*time.Second, "tempo de simulacao")
	data := flag.String("data", "20/11/2026", "data das caronas (DD/MM/AAAA)")
	semear := flag.Bool("semear", false, "publica caronas em cadeia entre as empresas antes de simular")
	caronasPorEmpresa := flag.Int("caronas", 5, "caronas publicadas por empresa ao semear")
	assentos := flag.Int("assentos", 4, "assentos por trecho de cada carona ao semear")
	cancelar := flag.Float64("cancelar", 0.2, "probabilidade de o passageiro cancelar uma reserva confirmada")
	progresso := flag.Duration("progresso", 2*time.Second, "intervalo entre as linhas de andamento")
	flag.Parse()

	var enderecos []string
	for _, e := range strings.Split(*servidores, ",") {
		if e = strings.TrimSpace(e); e != "" {
			enderecos = append(enderecos, e)
		}
	}
	if len(enderecos) == 0 || len(enderecos) >= len(cidades) {
		fmt.Println("Erro: informe entre 1 e", len(cidades)-1, "servidores")
		os.Exit(1)
	}

	if *semear {
		if err := semeia(enderecos, *data, *caronasPorEmpresa, *assentos); err != nil {
			fmt.Println("Erro ao semear caronas:", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Simulando %d passageiros em %d empresas por %v...\n", *passageiros, len(enderecos), *duracao)
	m := &Metricas{contagem: make(map[string]int), latencias: make(map[string][]time.Duration)}
	fim := time.Now().Add(*duracao)
	inicio := time.Now()
	parar := make(chan struct{})
	go m.mostraProgresso(inicio, *duracao, *progresso, parar)

	var wg sync.WaitGroup
	for i := 0; i < *passageiros; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			passageiro(i, enderecos[i%len(enderecos)], len(enderecos), *data, *cancelar, fim, m)
		}(i)
	}
	wg.Wait()
	close(parar)

	relatorio(m, time.Since(inicio))
}

// Publica, em cada empresa i, caronas de cidades[i] para cidades[i+1]
func semeia(enderecos []string, data string, quantidade, assentos int) error {
	for i, endereco := range enderecos {
		c, err := cliente.Conecta(endereco, "motorista-simulador", "simulador")
		if err != nil {
			return fmt.Errorf("%s: %v", endereco, err)
		}
		for j := 0; j < quantidade; j++ {
			var resp protocolo.PublicarCaronaResp
			err := c.Chama(protocolo.PublicarCarona, protocolo.PublicarCaronaReq{
				Rota:              []string{cidades[i], cidades[i+1]},
				Data:              data,
				PrecoPorTrecho:    []float64{float64(20 + 5*j)},
				AssentosPorTrecho: []int{assentos},
			}, &resp)
			if err != nil {
				c.Fecha()
				return fmt.Errorf("%s: %v", endereco, err)
			}
		}
		c.Fecha()
		fmt.Printf("Semeadas %d caronas %s -> %s em %s\n", quantidade, cidades[i], cidades[i+1], endereco)
	}
	return nil
}

// Um passageiro: busca um trecho aleatório da cadeia, reserva um dos itinerários
// encontrados e às vezes cancela, até o tempo acabar. Reconecta se o servidor cair.
func passageiro(i int, endereco string, numEmpresas int, data string, probCancelar float64, fim time.Time, m *Metricas) {
	sorteio := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))
	nome := fmt.Sprintf("passageiro-sim-%d", i)
	var c *cliente.Cliente
	defer func() {
		if c != nil {
			c.Fecha()
		}
	}()

	for time.Now().Before(fim) {
		if c == nil {
			var err error
			if c, err = cliente.Conecta(endereco, nome, "simulador"); err != nil {
				m.registra("conexao", "falhou", 0)
				c = nil
				time.Sleep(500 * time.Millisecond)
				continue
			}
		}

		// Origem e destino aleatórios ao longo da cadeia (podem envolver uma ou várias empresas)
		a := sorteio.Intn(numEmpresas)
		b := a + 1 + sorteio.Intn(numEmpresas-a)
		inicio := time.Now()
		resp, err := c.Envia(protocolo.BuscarItinerarios, protocolo.BuscarItinerariosReq{
			Origem: cidades[a], Destino: cidades[b], Data: data,
		})
		if err != nil {
			m.registra("busca", "conexao caiu", 0)
			c.Fecha()
			c = nil
			continue
		}
		var busca protocolo.BuscarItinerariosResp
		if !resp.Ok || decodifica(resp, &busca) != nil || len(busca.Itinerarios) == 0 {
			m.registra("busca", "sem itinerario", time.Since(inicio))
			time.Sleep(200 * time.Millisecond)
			continue
		}
		m.registra("busca", "ok", time.Since(inicio))

		escolhido := busca.Itinerarios[sorteio.Intn(len(busca.Itinerarios))]
		inicio = time.Now()
		resp, err = c.Envia(protocolo.Reservar, protocolo.ReservarReq{Itens: escolhido.Itens})
		if err != nil {
			m.registra("reserva", "conexao caiu", 0)
			c.Fecha()
			c = nil
			continue
		}
		if !resp.Ok {
			m.registra("reserva", "recusada", time.Since(inicio))
			continue
		}
		m.registra("reserva", fmt.Sprintf("confirmada (%d empresa(s))", empresasDistintas(escolhido.Itens)), time.Since(inicio))

		if sorteio.Float64() < probCancelar {
			var feita protocolo.ReservarResp
			decodifica(resp, &feita)
			inicio = time.Now()
			resp, err = c.Envia(protocolo.CancelarReserva, protocolo.CancelarReservaReq{ReservaID: feita.ReservaID})
			if err != nil {
				m.registra("cancelamento", "conexao caiu", 0)
				c.Fecha()
				c = nil
				continue
			}
			if resp.Ok {
				m.registra("cancelamento", "ok", time.Since(inicio))
			} else {
				m.registra("cancelamento", "recusado", time.Since(inicio))
			}
		}
	}
}

func decodifica(resp protocolo.Resposta, v interface{}) error {
	return json.Unmarshal(resp.Dados, v)
}

func empresasDistintas(itens []protocolo.ItemPedido) int {
	vistas := make(map[string]bool)
	for _, it := range itens {
		vistas[it.Empresa] = true
	}
	return len(vistas)
}

// Mostra a contagem de cada resultado e os tempos de resposta de cada operação
func relatorio(m *Metricas, total time.Duration) {
	fmt.Printf("\n===== Resultado da simulacao (%v) =====\n", total.Round(time.Millisecond))
	chaves := make([]string, 0, len(m.contagem))
	operacoes := 0
	for k, v := range m.contagem {
		chaves = append(chaves, k)
		operacoes += v
	}
	sort.Strings(chaves)
	for _, k := range chaves {
		fmt.Printf("  %-45s %6d\n", k, m.contagem[k])
	}
	fmt.Printf("  %-45s %6.1f ops/s\n", "vazao", float64(operacoes)/total.Seconds())

	fmt.Println("\nTempo de resposta:")
	fmt.Printf("  %-14s %8s %10s %10s %10s %10s\n", "operacao", "qtd", "media", "p50", "p95", "max")
	for _, op := range []string{"busca", "reserva", "cancelamento"} {
		l := m.latencias[op]
		if len(l) == 0 {
			continue
		}
		sort.Slice(l, func(i, j int) bool { return l[i] < l[j] })
		var soma time.Duration
		for _, d := range l {
			soma += d
		}
		fmt.Printf("  %-14s %8d %10v %10v %10v %10v\n", op, len(l),
			(soma / time.Duration(len(l))).Round(time.Microsecond),
			l[len(l)/2].Round(time.Microsecond),
			l[len(l)*95/100].Round(time.Microsecond),
			l[len(l)-1].Round(time.Microsecond))
	}
}
