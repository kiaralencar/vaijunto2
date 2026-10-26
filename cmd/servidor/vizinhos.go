package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	intervaloVerificacao = 5 * time.Second // Intervalo entre as verificações de cada vizinho
	timeoutVizinho       = 2 * time.Second // Tempo máximo de espera pela resposta de um vizinho
)

// Cliente HTTP com timeout, usado nas requisições aos vizinhos
var clienteHTTP = &http.Client{Timeout: timeoutVizinho}

// Vizinhos guarda os endereços configurados e qual empresa responde em cada um
type Vizinhos struct {
	mu        sync.Mutex
	enderecos []string
	empresaDe map[string]string // endereço -> empresa
}

// Cria a lista de vizinhos a partir dos endereços das flags
func NovosVizinhos(enderecos []string) *Vizinhos {
	return &Vizinhos{enderecos: enderecos, empresaDe: make(map[string]string)}
}

// Devolve os endereços configurados
func (v *Vizinhos) Enderecos() []string {
	return append([]string(nil), v.enderecos...)
}

// Devolve o endereço de uma empresa. Se ainda não for conhecido, pergunta o
// status a cada vizinho até encontrá-la.
func (v *Vizinhos) Endereco(s *Servidor, empresa string) (string, bool) {
	v.mu.Lock()
	for endereco, nome := range v.empresaDe {
		if nome == empresa {
			v.mu.Unlock()
			return endereco, true
		}
	}
	v.mu.Unlock()

	for _, endereco := range v.enderecos {
		nome, err := s.consultaStatus(endereco)
		if err != nil {
			continue
		}
		v.registra(endereco, nome)
		if nome == empresa {
			return endereco, true
		}
	}
	return "", false
}

func (v *Vizinhos) registra(endereco, empresa string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.empresaDe[endereco] = empresa
}

// Dispara uma goroutine de monitoramento para cada vizinho
func (v *Vizinhos) Monitora(s *Servidor) {
	for _, endereco := range v.enderecos {
		go v.monitoraVizinho(s, endereco)
	}
}

// Consulta o status do vizinho periodicamente e avisa no log quando a situação dele muda
func (v *Vizinhos) monitoraVizinho(s *Servidor, endereco string) {
	primeira := true
	ativoAntes := false
	for {
		empresa, err := s.consultaStatus(endereco)
		ativoAgora := err == nil
		if ativoAgora {
			v.registra(endereco, empresa)
		}

		if primeira || ativoAgora != ativoAntes {
			if ativoAgora {
				fmt.Printf("[vizinho] %s (%s) esta ativo\n", empresa, endereco)
			} else {
				fmt.Printf("[vizinho] %s inacessivel: %v\n", endereco, err)
			}
		}
		primeira, ativoAntes = false, ativoAgora
		time.Sleep(intervaloVerificacao)
	}
}

// Faz GET /status no vizinho e devolve o ID da empresa dele
func (s *Servidor) consultaStatus(endereco string) (string, error) {
	var st RespostaStatus
	status, err := s.requisicaoURL(http.MethodGet, endereco+"/status", nil, &st)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("status inesperado: %d", status)
	}
	return st.Empresa, nil
}

// Faz uma requisição REST a outra empresa, identificada pelo ID
func (s *Servidor) requisicao(empresa, metodo, caminho string, corpo, resposta interface{}) (int, error) {
	endereco, conhecido := s.vizinhos.Endereco(s, empresa)
	if !conhecido {
		return 0, errIndisponivel
	}
	return s.requisicaoURL(metodo, endereco+caminho, corpo, resposta)
}

// Faz uma requisição REST levando o relógio de Lamport no cabeçalho e
// atualizando o relógio local com o que vier na resposta
func (s *Servidor) requisicaoURL(metodo, url string, corpo, resposta interface{}) (int, error) {
	var leitor io.Reader
	if corpo != nil {
		dados, err := json.Marshal(corpo)
		if err != nil {
			return 0, err
		}
		leitor = bytes.NewReader(dados)
	}
	req, err := http.NewRequest(metodo, url, leitor)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(cabecalhoLamport, strconv.FormatInt(s.relogio.Tick(), 10))

	resp, err := clienteHTTP.Do(req)
	if err != nil {
		return 0, err // Conexão recusada ou timeout
	}
	defer resp.Body.Close() // Fecha o corpo da resposta ao retornar

	s.relogio.LeCabecalho(resp.Header)
	if resposta != nil {
		json.NewDecoder(resp.Body).Decode(resposta)
	}
	return resp.StatusCode, nil
}
