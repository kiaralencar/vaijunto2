package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config guarda a identificação desta instância no consórcio
type Config struct {
	Empresa       string        // ID da empresa
	PortaTCP      string        // Porta dos clientes motorista/passageiro
	PortaHTTP     string        // Porta da API REST entre servidores
	Vizinhos      []string      // Endereços HTTP dos servidores vizinhos
	DirDados      string        // Pasta onde a empresa grava o seu estado
	PrazoDecisao  time.Duration // Tempo que um assento fica segurado esperando a decisão
	AtrasoDecisao time.Duration // Atraso entre as fases da reserva (injeção de falhas)
}

// Lê as flags da linha de comando e monta a configuração do servidor
func lerConfig() Config {
	empresa := flag.String("empresa", "", "identificador desta empresa no consorcio (obrigatorio)")
	portaTCP := flag.String("porta-tcp", "8080", "porta TCP dos clientes motorista/passageiro")
	portaHTTP := flag.String("porta-http", "9090", "porta HTTP da API REST entre servidores")
	vizinhos := flag.String("vizinhos", "", "URLs dos vizinhos separadas por virgula")
	dados := flag.String("dados", "dados", "pasta de dados (cada empresa usa uma subpasta com o seu ID; vazio desliga a gravacao)")
	prazo := flag.Duration("prazo-decisao", 10*time.Second, "tempo que um assento fica segurado esperando a decisao")
	atraso := flag.Duration("atraso-decisao", 0, "atraso entre as fases da reserva, para testes de falha")
	flag.Parse()

	if *empresa == "" {
		fmt.Println("Erro: a flag -empresa e obrigatoria")
		flag.Usage()
		os.Exit(1)
	}
	if strings.ContainsAny(*empresa, "/\\ ") {
		fmt.Println("Erro: o ID da empresa nao pode ter espacos nem barras")
		os.Exit(1)
	}

	cfg := Config{
		Empresa:       *empresa,
		PortaTCP:      *portaTCP,
		PortaHTTP:     *portaHTTP,
		PrazoDecisao:  *prazo,
		AtrasoDecisao: *atraso,
	}
	if *dados != "" {
		cfg.DirDados = filepath.Join(*dados, *empresa)
	}

	// Separa os vizinhos pela vírgula, ignorando entradas vazias
	for _, v := range strings.Split(*vizinhos, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			cfg.Vizinhos = append(cfg.Vizinhos, strings.TrimSuffix(v, "/"))
		}
	}
	return cfg
}
