package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	intervaloVerificacao = 5 * time.Second // Intervalo entre as verificações de cada vizinho
	timeoutVizinho       = 2 * time.Second // Tempo máximo de espera pela resposta de um vizinho
)

// Cliente HTTP com timeout, usado nas requisições aos vizinhos
var clienteHTTP = &http.Client{Timeout: timeoutVizinho}

// Dispara uma goroutine de monitoramento para cada vizinho
func iniciaMonitorVizinhos(cfg Config) {
	for _, endereco := range cfg.Vizinhos {
		go monitoraVizinho(endereco)
	}
}

// Consulta o status do vizinho periodicamente e avisa no log quando a situação dele muda
func monitoraVizinho(endereco string) {
	primeira := true
	ativoAntes := false
	for {
		empresa, err := consultaStatus(endereco)
		ativoAgora := err == nil

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
func consultaStatus(endereco string) (string, error) {
	resp, err := clienteHTTP.Get(endereco + "/status")
	if err != nil {
		return "", err // Conexão recusada ou timeout
	}
	defer resp.Body.Close() // Fecha o corpo da resposta ao retornar

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status inesperado: %s", resp.Status)
	}
	var st RespostaStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "", fmt.Errorf("resposta invalida: %v", err)
	}
	return st.Empresa, nil
}
