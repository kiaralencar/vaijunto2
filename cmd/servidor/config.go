package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Config guarda a identificação desta instância no consórcio
type Config struct {
	Empresa   string   // ID da empresa
	PortaTCP  string   // Porta dos clientes motorista/passageiro
	PortaHTTP string   // Porta da API REST entre servidores
	Vizinhos  []string // Endereços HTTP dos servidores vizinhos
}

// Lê as flags da linha de comando e monta a configuração do servidor
func lerConfig() Config {
	empresa := flag.String("empresa", "", "identificador desta empresa no consorcio (obrigatorio)")
	portaTCP := flag.String("porta-tcp", "8080", "porta TCP dos clientes motorista/passageiro")
	portaHTTP := flag.String("porta-http", "9090", "porta HTTP da API REST entre servidores")
	vizinhos := flag.String("vizinhos", "", "URLs dos vizinhos separadas por virgula")
	flag.Parse()

	if *empresa == "" {
		fmt.Println("Erro: a flag -empresa e obrigatoria")
		flag.Usage()
		os.Exit(1)
	}

	cfg := Config{Empresa: *empresa, PortaTCP: *portaTCP, PortaHTTP: *portaHTTP}

	// Separa os vizinhos pela vírgula, ignorando entradas vazias
	for _, v := range strings.Split(*vizinhos, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			cfg.Vizinhos = append(cfg.Vizinhos, v)
		}
	}
	return cfg
}
