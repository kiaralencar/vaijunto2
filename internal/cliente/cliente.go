// Package cliente é um cliente mínimo do protocolo TCP do VaiJunto (o mesmo
// dos clientes motorista e passageiro), usado pelo simulador e pelos testes.
package cliente

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"vaijunto/internal/protocolo"
)

// Tempo máximo de espera por uma resposta do servidor
const timeoutResposta = 30 * time.Second

// Cliente é uma conexão aberta com o servidor de uma empresa
type Cliente struct {
	conn   net.Conn
	leitor *bufio.Reader
}

// Conecta no servidor e faz login
func Conecta(endereco, nome, senha string) (*Cliente, error) {
	conn, err := net.DialTimeout("tcp", endereco, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c := &Cliente{conn: conn, leitor: bufio.NewReader(conn)}
	resp, err := c.Envia(protocolo.Login, protocolo.LoginReq{Nome: nome, Senha: senha})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !resp.Ok {
		conn.Close()
		return nil, fmt.Errorf("login recusado: %s", resp.Erro)
	}
	return c, nil
}

// Envia um pedido e espera a resposta
func (c *Cliente) Envia(tipo string, dados interface{}) (protocolo.Resposta, error) {
	corpo, err := json.Marshal(dados)
	if err != nil {
		return protocolo.Resposta{}, err
	}
	linha, err := json.Marshal(protocolo.Pedido{Tipo: tipo, Dados: corpo})
	if err != nil {
		return protocolo.Resposta{}, err
	}
	c.conn.SetDeadline(time.Now().Add(timeoutResposta))
	if _, err := c.conn.Write(append(linha, '\n')); err != nil {
		return protocolo.Resposta{}, err
	}
	respLinha, err := c.leitor.ReadString('\n')
	if err != nil {
		return protocolo.Resposta{}, err
	}
	var resp protocolo.Resposta
	if err := json.Unmarshal([]byte(respLinha), &resp); err != nil {
		return protocolo.Resposta{}, err
	}
	return resp, nil
}

// Envia um pedido e decodifica os dados da resposta em "saida"
func (c *Cliente) Chama(tipo string, dados, saida interface{}) error {
	resp, err := c.Envia(tipo, dados)
	if err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("%s", resp.Erro)
	}
	if saida != nil {
		return json.Unmarshal(resp.Dados, saida)
	}
	return nil
}

// Fecha a conexão
func (c *Cliente) Fecha() {
	c.conn.Close()
}
