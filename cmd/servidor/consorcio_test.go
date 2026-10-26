package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/cliente"
	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// Estes testes sobem cada empresa como um processo separado (o binário real
// do servidor) e derrubam processos no meio da carga com Kill, que equivale a
// uma queda abrupta: nada é encerrado de forma organizada.

const dataTeste = "20/11/2026"

var (
	binarioOnce sync.Once
	binario     string
	binarioErr  error
)

// Compila o servidor uma única vez para todos os testes
func compilaServidor(t *testing.T) string {
	t.Helper()
	binarioOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vaijunto-teste")
		if err != nil {
			binarioErr = err
			return
		}
		binario = filepath.Join(dir, "servidor")
		if runtime.GOOS == "windows" {
			binario += ".exe"
		}
		if saida, err := exec.Command("go", "build", "-o", binario, ".").CombinedOutput(); err != nil {
			binarioErr = fmt.Errorf("%v: %s", err, saida)
		}
	})
	if binarioErr != nil {
		t.Fatalf("erro ao compilar o servidor: %v", binarioErr)
	}
	return binario
}

// Saída do processo, protegida para ser lida enquanto ele escreve
type logSeguro struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logSeguro) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logSeguro) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// instancia é o processo do servidor de uma empresa
type instancia struct {
	empresa   string
	portaTCP  int
	portaHTTP int
	args      []string
	cmd       *exec.Cmd
	log       *logSeguro
}

func (in *instancia) enderecoTCP() string { return fmt.Sprintf("127.0.0.1:%d", in.portaTCP) }
func (in *instancia) url() string         { return fmt.Sprintf("http://127.0.0.1:%d", in.portaHTTP) }

// Sobe o processo e espera a API responder
func (in *instancia) inicia(t *testing.T) {
	t.Helper()
	in.log = &logSeguro{}
	in.cmd = exec.Command(compilaServidor(t), in.args...)
	in.cmd.Stdout = in.log
	in.cmd.Stderr = in.log
	if err := in.cmd.Start(); err != nil {
		t.Fatalf("erro ao iniciar %s: %v", in.empresa, err)
	}
	limite := time.Now().Add(15 * time.Second)
	for time.Now().Before(limite) {
		if resp, err := http.Get(in.url() + "/status"); err == nil {
			resp.Body.Close()
			if c, err := net.Dial("tcp", in.enderecoTCP()); err == nil {
				c.Close()
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s nao ficou pronto. Log:\n%s", in.empresa, in.log.String())
}

// Derruba o processo de forma abrupta
func (in *instancia) derruba() {
	if in.cmd != nil && in.cmd.Process != nil {
		in.cmd.Process.Kill()
		in.cmd.Wait()
	}
}

// Fotografia do estado da empresa
func (in *instancia) auditoria() (Auditoria, error) {
	var a Auditoria
	resp, err := http.Get(in.url() + "/auditoria")
	if err != nil {
		return a, err
	}
	defer resp.Body.Close()
	return a, json.NewDecoder(resp.Body).Decode(&a)
}

func portaLivre(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sem porta livre: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Sobe um consórcio em malha completa. "extras" acrescenta flags por empresa.
func sobeConsorcio(t *testing.T, empresas []string, extras map[string][]string) map[string]*instancia {
	t.Helper()
	dados := t.TempDir()
	consorcio := make(map[string]*instancia)
	for _, e := range empresas {
		consorcio[e] = &instancia{empresa: e, portaTCP: portaLivre(t), portaHTTP: portaLivre(t)}
	}
	for _, e := range empresas {
		var vizinhos []string
		for _, outra := range empresas {
			if outra != e {
				vizinhos = append(vizinhos, consorcio[outra].url())
			}
		}
		in := consorcio[e]
		in.args = append([]string{
			"-empresa=" + e,
			"-porta-tcp=" + strconv.Itoa(in.portaTCP),
			"-porta-http=" + strconv.Itoa(in.portaHTTP),
			"-vizinhos=" + strings.Join(vizinhos, ","),
			"-dados=" + dados,
			"-prazo-decisao=3s",
		}, extras[e]...)
	}
	for _, e := range empresas {
		consorcio[e].inicia(t)
	}
	t.Cleanup(func() {
		for _, in := range consorcio {
			in.derruba()
		}
	})
	return consorcio
}

// Publica uma carona como motorista da empresa e devolve o ID
func publica(t *testing.T, in *instancia, rota []string, assentos int) string {
	t.Helper()
	c, err := cliente.Conecta(in.enderecoTCP(), "motorista-"+in.empresa, "x")
	if err != nil {
		t.Fatalf("erro ao conectar em %s: %v", in.empresa, err)
	}
	defer c.Fecha()
	precos := make([]float64, len(rota)-1)
	vagas := make([]int, len(rota)-1)
	for i := range precos {
		precos[i], vagas[i] = 10, assentos
	}
	var resp protocolo.PublicarCaronaResp
	if err := c.Chama(protocolo.PublicarCarona, protocolo.PublicarCaronaReq{
		Rota: rota, Data: dataTeste, PrecoPorTrecho: precos, AssentosPorTrecho: vagas,
	}, &resp); err != nil {
		t.Fatalf("erro ao publicar em %s: %v", in.empresa, err)
	}
	return resp.CaronaID
}

// Espera não haver mais reservas em andamento, assentos segurados nem mensagens pendentes
func esperaConvergencia(t *testing.T, consorcio map[string]*instancia, limite time.Duration) map[string]Auditoria {
	t.Helper()
	fim := time.Now().Add(limite)
	for {
		auds := make(map[string]Auditoria)
		pendente := ""
		for nome, in := range consorcio {
			a, err := in.auditoria()
			if err != nil {
				pendente = nome + " inacessivel"
				break
			}
			auds[nome] = a
			for _, r := range a.Reservas {
				if r.Estado == dominio.Preparando {
					pendente = fmt.Sprintf("%s: reserva %s preparando", nome, r.ID)
				}
			}
			for _, p := range a.Participacoes {
				if p.Estado == dominio.Preparada {
					pendente = fmt.Sprintf("%s: assento segurado para %s", nome, p.ID)
				}
			}
			if a.Pendentes > 0 {
				pendente = fmt.Sprintf("%s: %d mensagens pendentes", nome, a.Pendentes)
			}
		}
		if pendente == "" {
			return auds
		}
		if time.Now().After(fim) {
			for nome, in := range consorcio {
				t.Logf("log de %s:\n%s", nome, ultimasLinhas(in.log.String(), 30))
			}
			t.Fatalf("o consorcio nao convergiu em %v: %s", limite, pendente)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func ultimasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimSpace(s), "\n")
	if len(linhas) > n {
		linhas = linhas[len(linhas)-n:]
	}
	return strings.Join(linhas, "\n")
}

// Confere os três invariantes do enunciado sobre o estado final de todas as empresas
func verificaInvariantes(t *testing.T, auds map[string]Auditoria) (confirmadas int) {
	t.Helper()

	participacoes := make(map[string]map[string]dominio.Participacao) // empresa -> reserva -> participação
	for nome, a := range auds {
		participacoes[nome] = make(map[string]dominio.Participacao)
		for _, p := range a.Participacoes {
			participacoes[nome][p.ID] = p
			if p.Estado == dominio.Preparada {
				t.Errorf("assento travado: %s ainda segura assento para %s", nome, p.ID)
			}
		}
	}

	// 1) Nenhum assento confirmado duas vezes: em cada trecho, ocupados = confirmados <= capacidade
	for nome, a := range auds {
		for _, c := range a.Caronas {
			for tr := range c.AssentosTotais {
				confirmadosNoTrecho := 0
				for _, p := range participacoes[nome] {
					if p.Estado != dominio.Confirmada {
						continue
					}
					for _, item := range p.Itens {
						if item.CaronaID == c.ID && item.TrechoInicio <= tr && tr <= item.TrechoFim {
							confirmadosNoTrecho++
						}
					}
				}
				ocupados := c.AssentosTotais[tr] - c.AssentosLivres[tr]
				if c.AssentosLivres[tr] < 0 || confirmadosNoTrecho > c.AssentosTotais[tr] {
					t.Errorf("assento vendido duas vezes: %s trecho %d tem %d confirmados para %d assentos",
						c.ID, tr, confirmadosNoTrecho, c.AssentosTotais[tr])
				}
				if c.Ativa && ocupados != confirmadosNoTrecho {
					t.Errorf("assento travado ou perdido: %s trecho %d tem %d ocupados mas %d confirmados",
						c.ID, tr, ocupados, confirmadosNoTrecho)
				}
			}
		}
	}

	// 2) Nenhum itinerário pela metade: a decisão do coordenador vale em todas as empresas do itinerário
	coordenadas := make(map[string]bool)
	for nome, a := range auds {
		for _, r := range a.Reservas {
			coordenadas[r.ID] = true
			for _, empresa := range empresasDaReserva(r) {
				p, existe := participacoes[empresa][r.ID]
				switch r.Estado {
				case dominio.Confirmada:
					if !existe || p.Estado != dominio.Confirmada {
						t.Errorf("itinerario pela metade: %s confirmou %s, mas em %s esta %q", nome, r.ID, empresa, p.Estado)
					}
				default:
					if existe && p.Estado == dominio.Confirmada {
						t.Errorf("itinerario pela metade: %s esta %s, mas %s confirmou o trecho", r.ID, r.Estado, empresa)
					}
				}
			}
			if r.Estado == dominio.Confirmada {
				confirmadas++
			}
		}
	}

	// Nenhuma participação confirmada sem uma reserva confirmada no coordenador
	for nome, ps := range participacoes {
		for id, p := range ps {
			if p.Estado == dominio.Confirmada && !coordenadas[id] {
				t.Errorf("%s confirmou %s, mas nenhum coordenador conhece essa reserva", nome, id)
			}
		}
	}
	return confirmadas
}

// Carga de passageiros em todas as empresas disputando itinerários que cruzam
// as três, com uma das empresas derrubada e religada no meio da carga.
func TestConsorcioCargaComQuedaDeServidor(t *testing.T) {
	empresas := []string{"alfa", "beta", "gama"}
	atraso := []string{"-atraso-decisao=30ms"} // Alarga a janela entre as fases, para a queda pegar reservas no meio
	consorcio := sobeConsorcio(t, empresas, map[string][]string{"alfa": atraso, "beta": atraso, "gama": atraso})

	// Itinerário A -> D só existe combinando as três empresas
	for i := 0; i < 3; i++ {
		publica(t, consorcio["alfa"], []string{"A", "B"}, 3)
		publica(t, consorcio["beta"], []string{"B", "C"}, 3)
		publica(t, consorcio["gama"], []string{"C", "D"}, 3)
	}
	publica(t, consorcio["beta"], []string{"A", "B", "C"}, 2)

	const numPassageiros = 60
	var mu sync.Mutex
	resultados := map[string]int{}
	conta := func(chave string) {
		mu.Lock()
		resultados[chave]++
		mu.Unlock()
	}

	// Cada passageiro tenta várias vezes ao longo de alguns segundos: se o servidor
	// dele cair, reconecta; se não houver itinerário ou a reserva for recusada, tenta de novo
	var wg sync.WaitGroup
	for i := 0; i < numPassageiros; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := consorcio[empresas[i%len(empresas)]]
			nome := fmt.Sprintf("passageiro-%d", i)
			sorteio := rand.New(rand.NewSource(int64(i)))
			var c *cliente.Cliente
			defer func() {
				if c != nil {
					c.Fecha()
				}
			}()
			for tentativa := 0; tentativa < 20; tentativa++ {
				time.Sleep(time.Duration(sorteio.Intn(300)) * time.Millisecond)
				if c == nil {
					var err error
					if c, err = cliente.Conecta(in.enderecoTCP(), nome, "x"); err != nil {
						conta("tentativa sem conexao")
						c = nil
						continue
					}
				}
				var busca protocolo.BuscarItinerariosResp
				resp, err := c.Envia(protocolo.BuscarItinerarios, protocolo.BuscarItinerariosReq{Origem: "A", Destino: "D", Data: dataTeste})
				if err != nil {
					conta("conexao caiu")
					c.Fecha()
					c = nil
					continue
				}
				json.Unmarshal(resp.Dados, &busca)
				if len(busca.Itinerarios) == 0 {
					conta("busca sem itinerario")
					continue
				}
				escolhido := busca.Itinerarios[sorteio.Intn(len(busca.Itinerarios))]
				resp, err = c.Envia(protocolo.Reservar, protocolo.ReservarReq{Itens: escolhido.Itens})
				if err != nil {
					conta("conexao caiu")
					c.Fecha()
					c = nil
					continue
				}
				if resp.Ok {
					conta("confirmada")
					return
				}
				conta("reserva recusada")
			}
			conta("desistiu")
		}(i)
	}

	// Injeção de falha: derruba a beta no meio da carga e religa com os mesmos dados
	time.Sleep(300 * time.Millisecond)
	consorcio["beta"].derruba()
	t.Log("beta derrubada no meio da carga")
	time.Sleep(1 * time.Second)
	consorcio["beta"].inicia(t)
	t.Log("beta religada")

	wg.Wait()
	t.Logf("resultado dos %d passageiros: %v", numPassageiros, resultados)

	auds := esperaConvergencia(t, consorcio, 40*time.Second)
	for nome, in := range consorcio {
		for _, linha := range strings.Split(in.log.String(), "\n") {
			if strings.HasPrefix(linha, "[recuperacao]") || strings.HasPrefix(linha, "[prazo]") {
				t.Logf("%s: %s", nome, linha)
			}
		}
	}
	confirmadas := verificaInvariantes(t, auds)
	t.Logf("reservas confirmadas no consorcio: %d", confirmadas)
	if confirmadas == 0 {
		t.Errorf("nenhuma reserva foi confirmada: o teste nao exercitou o caminho de sucesso")
	}
}

// O coordenador cai depois de preparar em outra empresa e antes de decidir:
// o assento segurado na outra empresa não pode ficar travado.
func TestQuedaDoCoordenadorNoMeioDaReserva(t *testing.T) {
	consorcio := sobeConsorcio(t, []string{"alfa", "beta"}, map[string][]string{"alfa": {"-atraso-decisao=3s"}})
	ab := publica(t, consorcio["alfa"], []string{"A", "B"}, 1)
	bc := publica(t, consorcio["beta"], []string{"B", "C"}, 1)

	go func() {
		c, err := cliente.Conecta(consorcio["alfa"].enderecoTCP(), "passageiro", "x")
		if err != nil {
			return
		}
		defer c.Fecha()
		c.Envia(protocolo.Reservar, protocolo.ReservarReq{Itens: []protocolo.ItemPedido{
			{CaronaID: ab, TrechoInicio: 0, TrechoFim: 0},
			{CaronaID: bc, TrechoInicio: 0, TrechoFim: 0},
		}})
	}()

	// Espera a beta segurar o assento (fase 1 concluída) e derruba a alfa antes da decisão
	esperaParticipacao(t, consorcio["beta"], dominio.Preparada)
	consorcio["alfa"].derruba()
	t.Log("alfa (coordenador) derrubada com o assento segurado na beta")

	// Com a alfa fora do ar, a beta continua atendendo, mas não pode decidir sozinha
	time.Sleep(4 * time.Second)
	if a, err := consorcio["beta"].auditoria(); err != nil {
		t.Fatalf("beta deveria continuar no ar: %v", err)
	} else if a.Participacoes[0].Estado != dominio.Preparada {
		t.Fatalf("sem o coordenador, a beta nao deveria decidir sozinha, mas a participacao esta %q", a.Participacoes[0].Estado)
	}

	// Ao voltar, a alfa aborta a reserva sem decisão e a beta libera o assento
	consorcio["alfa"].args = append(consorcio["alfa"].args, "-atraso-decisao=0s")
	consorcio["alfa"].inicia(t)
	auds := esperaConvergencia(t, consorcio, 20*time.Second)
	verificaInvariantes(t, auds)
	if r := auds["alfa"].Reservas; len(r) != 1 || r[0].Estado != dominio.Abortada {
		t.Fatalf("a reserva deveria estar abortada no coordenador: %+v", r)
	}
	for _, c := range auds["beta"].Caronas {
		if c.AssentosLivres[0] != 1 {
			t.Fatalf("o assento da beta deveria ter sido liberado, livres=%v", c.AssentosLivres)
		}
	}
}

// O participante cai depois de aceitar a preparação: o coordenador confirma
// e a confirmação é entregue quando o participante volta.
func TestQuedaDoParticipanteDepoisDePreparar(t *testing.T) {
	consorcio := sobeConsorcio(t, []string{"alfa", "beta"}, map[string][]string{"alfa": {"-atraso-decisao=2s"}})
	ab := publica(t, consorcio["alfa"], []string{"A", "B"}, 1)
	bc := publica(t, consorcio["beta"], []string{"B", "C"}, 1)

	resultado := make(chan protocolo.Resposta, 1)
	go func() {
		c, err := cliente.Conecta(consorcio["alfa"].enderecoTCP(), "passageiro", "x")
		if err != nil {
			resultado <- protocolo.Resposta{Erro: err.Error()}
			return
		}
		defer c.Fecha()
		resp, _ := c.Envia(protocolo.Reservar, protocolo.ReservarReq{Itens: []protocolo.ItemPedido{
			{CaronaID: ab, TrechoInicio: 0, TrechoFim: 0},
			{CaronaID: bc, TrechoInicio: 0, TrechoFim: 0},
		}})
		resultado <- resp
	}()

	esperaParticipacao(t, consorcio["beta"], dominio.Preparada)
	consorcio["beta"].derruba()
	t.Log("beta (participante) derrubada depois de aceitar a preparacao")

	resp := <-resultado
	if !resp.Ok {
		t.Fatalf("todas as empresas aceitaram a preparacao, a reserva deveria ser confirmada: %s", resp.Erro)
	}

	consorcio["beta"].inicia(t)
	auds := esperaConvergencia(t, consorcio, 20*time.Second)
	if verificaInvariantes(t, auds) != 1 {
		t.Fatalf("a reserva deveria estar confirmada no consorcio")
	}
}

// Espera uma empresa ter alguma participação no estado dado
func esperaParticipacao(t *testing.T, in *instancia, estado string) {
	t.Helper()
	fim := time.Now().Add(10 * time.Second)
	for time.Now().Before(fim) {
		if a, err := in.auditoria(); err == nil {
			for _, p := range a.Participacoes {
				if p.Estado == estado {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s nao chegou a ter participacao %s. Log:\n%s", in.empresa, estado, in.log.String())
}
