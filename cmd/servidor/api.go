package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// Resposta do GET /status
type RespostaStatus struct {
	Empresa string `json:"empresa"`
	Status  string `json:"status"`
}

// Carona como é enviada às outras empresas (sem o motorista)
type CaronaREST struct {
	ID             string    `json:"id"`
	Rota           []string  `json:"rota"`
	Data           string    `json:"data"`
	PrecoPorTrecho []float64 `json:"preco_por_trecho"`
	AssentosLivres []int     `json:"assentos_livres"`
}

// Resposta do GET /caronas
type RespostaCaronas struct {
	Empresa string       `json:"empresa"`
	Caronas []CaronaREST `json:"caronas"`
}

// Sobe a API REST usada pelos outros servidores do consórcio
func iniciaAPI(cfg Config, estado *Estado) {
	mux := http.NewServeMux()

	// Informa o ID da empresa e que o servidor está ativo
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		escreveJSON(w, http.StatusOK, RespostaStatus{Empresa: cfg.Empresa, Status: "ativo"})
	})

	// Lista as caronas ativas desta empresa
	mux.HandleFunc("GET /caronas", func(w http.ResponseWriter, r *http.Request) {
		caronas := estado.ListarCaronas()
		lista := make([]CaronaREST, 0, len(caronas))
		for _, c := range caronas {
			lista = append(lista, CaronaREST{
				ID:             c.ID,
				Rota:           c.Rota,
				Data:           c.Data,
				PrecoPorTrecho: c.PrecoPorTrecho,
				AssentosLivres: c.AssentosLivres,
			})
		}
		escreveJSON(w, http.StatusOK, RespostaCaronas{Empresa: cfg.Empresa, Caronas: lista})
	})

	if err := http.ListenAndServe(":"+cfg.PortaHTTP, mux); err != nil {
		fmt.Println("Erro na API REST:", err)
		os.Exit(1)
	}
}

// Monta a resposta HTTP em JSON com o código de status dado
func escreveJSON(w http.ResponseWriter, codigo int, dados interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(codigo)
	json.NewEncoder(w).Encode(dados)
}
