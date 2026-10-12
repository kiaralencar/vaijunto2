// Servidor de uma empresa do consórcio VaiJunto. Atende os clientes motorista e
// passageiro por TCP (uma goroutine por cliente, protocolo do Problema 1) e oferece
// uma API REST por HTTP aos servidores das outras empresas. Também verifica
// periodicamente se os vizinhos estão ativos. O estado da empresa fica em memória,
// protegido por um mutex.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

const tempoLimiteInatividade = 1 * time.Hour

// Sessao guarda quem está logado nesta conexão específica. Cada goroutine
// tem a sua, ou seja, não é compartilhada, por isso não precisa de mutex
type Sessao struct {
	Nome string
}

func main() {
	cfg := lerConfig() // Lê as flags de configuração

	// Porta sem IP explícito = escuta em todos os endereços desta máquina.
	l, err := net.Listen("tcp", ":"+cfg.PortaTCP)
	if err != nil {
		fmt.Println("Erro ao escutar:", err)
		os.Exit(1)
	}
	defer l.Close()
	fmt.Printf("Empresa %s | clientes na porta TCP %s | API REST na porta %s | vizinhos: %v\n",
		cfg.Empresa, cfg.PortaTCP, cfg.PortaHTTP, cfg.Vizinhos)

	estado := NovoEstado()

	go iniciaAPI(cfg, estado)  // API REST entre servidores
	iniciaMonitorVizinhos(cfg) // Verifica periodicamente se os vizinhos estão ativos

	// Accept() bloqueia até alguém conectar.
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Erro ao aceitar conexao:", err)
			continue
		}

		// Dispara a goroutine e volta imediatamente ao topo do for, pronto pra aceitar 
		// o próximo cliente (permite atender várias pessoas ao mesmo tempo)
		go atendeCliente(conn, estado)
	}
}

// atendeCliente roda numa goroutine própria, uma por conexão. 
func atendeCliente(conn net.Conn, estado *Estado) { // O mesmo estado criado no main() é compartilhado entre todas as goroutines
	defer conn.Close() // Fechar a conexao ao retornar (desconexao ou erro)
	sessao := &Sessao{}
	leitor := bufio.NewReader(conn)

	fmt.Println("Nova conexao:", conn.RemoteAddr()) // Exibição de log no servidor para saber quem conectou

	for {
		// Reseta o prazo a cada mensagem
		conn.SetReadDeadline(time.Now().Add(tempoLimiteInatividade))

		linha, err := leitor.ReadString('\n')
		if err != nil {
			if netErr, ehErroDeRede := err.(net.Error); ehErroDeRede && netErr.Timeout() {
				fmt.Println("Cliente", sessao.Nome, "inativo por muito tempo. Encerrando conexao:", conn.RemoteAddr())
			} else {
				fmt.Println("Cliente", sessao.Nome, "desconectou:", conn.RemoteAddr())
			}
			return // Encerra só esta goroutine
		}

		resposta := processaLinha(linha, estado, sessao)

		saida, err := json.Marshal(resposta)
		if err != nil {
			continue
		}
		if _, err := conn.Write(append(saida, '\n')); err != nil {
			fmt.Println("Cliente", sessao.Nome, "erro ao enviar resposta, encerrando conexao:", conn.RemoteAddr())
			return
		}
	}
}
