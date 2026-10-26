package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Armazenamento grava cada registro num arquivo JSON próprio, dentro da pasta
// da empresa: um arquivo por carona, reserva e participação, mais um arquivo
// "meta" com as senhas. Diretório vazio desliga a persistência (usado nos
// testes em memória).
type Armazenamento struct {
	dir string
}

// Tipos de registro, cada um numa subpasta
const (
	tipoCarona       = "caronas"
	tipoReserva      = "reservas"
	tipoParticipacao = "participacoes"
)

// Cria as subpastas de dados da empresa
func NovoArmazenamento(dir string) (*Armazenamento, error) {
	a := &Armazenamento{dir: dir}
	if dir == "" {
		return a, nil
	}
	for _, tipo := range []string{tipoCarona, tipoReserva, tipoParticipacao} {
		if err := os.MkdirAll(filepath.Join(dir, tipo), 0o755); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// Grava o registro de forma atômica: escreve num temporário e renomeia por cima
func (a *Armazenamento) Grava(tipo, id string, v interface{}) {
	if a.dir == "" {
		return
	}
	dados, err := json.Marshal(v)
	if err != nil {
		fmt.Println("Erro ao serializar", tipo, id, ":", err)
		return
	}
	destino := a.caminho(tipo, id)
	tmp := destino + ".tmp"
	if err := os.WriteFile(tmp, dados, 0o644); err != nil {
		fmt.Println("Erro ao gravar", destino, ":", err)
		return
	}
	if err := os.Rename(tmp, destino); err != nil {
		fmt.Println("Erro ao gravar", destino, ":", err)
	}
}

// Lê todos os registros de um tipo, chamando "le" com o conteúdo de cada arquivo
func (a *Armazenamento) LeTodos(tipo string, le func(dados []byte) error) error {
	if a.dir == "" {
		return nil
	}
	arquivos, err := os.ReadDir(filepath.Join(a.dir, tipo))
	if err != nil {
		return err
	}
	for _, arq := range arquivos {
		if !strings.HasSuffix(arq.Name(), ".json") {
			continue // Ignora temporários de uma gravação interrompida
		}
		dados, err := os.ReadFile(filepath.Join(a.dir, tipo, arq.Name()))
		if err != nil {
			return err
		}
		if err := le(dados); err != nil {
			return fmt.Errorf("%s/%s: %v", tipo, arq.Name(), err)
		}
	}
	return nil
}

// Grava o arquivo meta (fora das subpastas)
func (a *Armazenamento) GravaMeta(v interface{}) {
	a.Grava("", "meta", v)
}

// Lê o arquivo meta, devolvendo false se ainda não existir
func (a *Armazenamento) LeMeta(v interface{}) (bool, error) {
	if a.dir == "" {
		return false, nil
	}
	dados, err := os.ReadFile(a.caminho("", "meta"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(dados, v)
}

func (a *Armazenamento) caminho(tipo, id string) string {
	return filepath.Join(a.dir, tipo, id+".json")
}
