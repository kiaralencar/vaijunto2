// Servidor de uma empresa do consórcio VaiJunto. Atende os clientes motorista e
// passageiro por TCP (uma goroutine por cliente, protocolo do Problema 1) e oferece
// uma API REST por HTTP aos servidores das outras empresas. Busca itinerários
// juntando as caronas dos vizinhos e coordena reservas atômicas entre empresas
// (preparar/confirmar, com precedência pelo relógio de Lamport). O estado da
// empresa fica em memória, protegido por um mutex, e é gravado em disco a cada
// alteração para ser recuperado quando o servidor volta de uma queda.
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

	s, err := NovoServidor(cfg) // Recupera o estado gravado, se houver
	if err != nil {
		fmt.Println("Erro ao iniciar:", err)
		os.Exit(1)
	}

	// Porta sem IP explícito = escuta em todos os endereços desta máquina.
	l, err := net.Listen("tcp", ":"+cfg.PortaTCP)
	if err != nil {
		fmt.Println("Erro ao escutar:", err)
		os.Exit(1)
	}
	defer l.Close()
	fmt.Printf("Empresa %s | clientes na porta TCP %s | API REST na porta %s | vizinhos: %v\n",
		cfg.Empresa, cfg.PortaTCP, cfg.PortaHTTP, cfg.Vizinhos)

	go s.IniciaAPI()  // API REST entre servidores
	s.IniciaRotinas() // Vizinhos, reenvio de mensagens e prazos das reservas

	// Accept() bloqueia até alguém conectar.
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Erro ao aceitar conexao:", err)
			continue
		}

		// Dispara a goroutine e volta imediatamente ao topo do for, pronto pra aceitar 
		// o próximo cliente (permite atender várias pessoas ao mesmo tempo)
		go atendeCliente(conn, s)
	}
}

// atendeCliente roda numa goroutine própria, uma por conexão.
func atendeCliente(conn net.Conn, s *Servidor) { // O mesmo servidor criado no main() é compartilhado entre todas as goroutines
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

		resposta := processaLinha(linha, s, sessao)

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
