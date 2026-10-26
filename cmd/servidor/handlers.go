package main

import (
	"encoding/json"

	"vaijunto/internal/dominio"
	"vaijunto/internal/protocolo"
)

// processaLinha decodifica uma linha no envelope Pedido e
// despacha para o handler certo pelo campo Tipo.
func processaLinha(linha string, s *Servidor, sessao *Sessao) protocolo.Resposta {
	var pedido protocolo.Pedido
	if err := json.Unmarshal([]byte(linha), &pedido); err != nil {
		// Mensagem malformada: responde com erro e não derruba a conexão nem o servidor.
		return protocolo.Resposta{Ok: false, Erro: "mensagem malformada: " + err.Error()}
	}

	switch pedido.Tipo {
	case protocolo.Login:
		return trataLogin(pedido, s, sessao)
	case protocolo.PublicarCarona:
		return trataPublicarCarona(pedido, s, sessao)
	case protocolo.ListarCaronas:
		return trataListarCaronas(pedido, s)
	case protocolo.Reservar:
		return trataReservar(pedido, s, sessao)
	case protocolo.CancelarReserva:
		return trataCancelarReserva(pedido, s, sessao)
	case protocolo.ListarMinhasReservas:
		return trataListarMinhasReservas(pedido, s, sessao)
	case protocolo.CancelarCarona:
		return trataCancelarCarona(pedido, s, sessao)
	case protocolo.ConsultarPassageiros:
		return trataConsultarPassageiros(pedido, s, sessao)
	case protocolo.BuscarItinerarios:
		return trataBuscarItinerarios(pedido, s)
	default:
		return erro(pedido, "tipo de operacao desconhecido: "+pedido.Tipo)
	}
}

// Monta uma resposta de erro para o pedido dado
func erro(pedido protocolo.Pedido, msg string) protocolo.Resposta {
	return protocolo.Resposta{Tipo: pedido.Tipo, ID: pedido.ID, Ok: false, Erro: msg}
}

// Monta uma resposta de sucesso para o pedido dado
func ok(pedido protocolo.Pedido, dados interface{}) protocolo.Resposta {
	b, err := json.Marshal(dados)
	if err != nil {
		return erro(pedido, "erro interno ao montar resposta: "+err.Error())
	}
	return protocolo.Resposta{Tipo: pedido.Tipo, ID: pedido.ID, Ok: true, Dados: b}
}

// exigeLogin é usado pelos handlers que precisam saber quem está pedindo.
func exigeLogin(pedido protocolo.Pedido, sessao *Sessao) (protocolo.Resposta, bool) {
	if sessao.Nome == "" {
		return erro(pedido, "faca login primeiro"), false
	}
	return protocolo.Resposta{}, true
}

// Trata o pedido de login, criando a senha se for a primeira vez que o nome é usado.
func trataLogin(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	var req protocolo.LoginReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil || req.Nome == "" {
		return erro(pedido, "nome invalido")
	}
	if err := s.estado.Autentica(req.Nome, req.Senha); err != nil {
		return erro(pedido, err.Error())
	}
	sessao.Nome = req.Nome
	return ok(pedido, struct{}{})
}

// Trata o pedido de publicar uma nova carona, criando a carona no estado.
func trataPublicarCarona(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	if resp, apto := exigeLogin(pedido, sessao); !apto {
		return resp
	}
	var req protocolo.PublicarCaronaReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil {
		return erro(pedido, "dados invalidos")
	}
	if len(req.Rota) < 2 {
		return erro(pedido, "rota precisa de pelo menos duas cidades")
	}
	numTrechos := len(req.Rota) - 1
	if len(req.PrecoPorTrecho) != numTrechos || len(req.AssentosPorTrecho) != numTrechos {
		return erro(pedido, "preco_por_trecho e assentos_por_trecho devem ter um item por trecho")
	}

	c := s.estado.PublicarCarona(sessao.Nome, req.Rota, req.Data, req.PrecoPorTrecho, req.AssentosPorTrecho)
	return ok(pedido, protocolo.PublicarCaronaResp{CaronaID: c.ID})
}

// Trata o pedido de listar as caronas ativas de todo o consórcio, retornando um resumo de cada uma.
func trataListarCaronas(pedido protocolo.Pedido, s *Servidor) protocolo.Resposta {
	caronas := s.caronasDoConsorcio("")
	lista := make([]dominio.Carona, len(caronas))
	empresaDe := make(map[string]string, len(caronas))
	for i, c := range caronas {
		lista[i] = c.Carona
		empresaDe[c.ID] = c.Empresa
	}
	ordenaCaronas(lista)

	resumos := make([]protocolo.CaronaResumo, 0, len(lista))
	for _, c := range lista {
		resumos = append(resumos, protocolo.CaronaResumo{
			ID:             c.ID,
			Empresa:        empresaDe[c.ID],
			Motorista:      c.Motorista,
			Rota:           c.Rota,
			Data:           c.Data,
			PrecoPorTrecho: c.PrecoPorTrecho,
			AssentosLivres: c.AssentosLivres,
		})
	}
	return ok(pedido, protocolo.ListarCaronasResp{Caronas: resumos})
}

// Trata o pedido de cancelar uma carona, verificando se o motorista é o dono da carona
func trataCancelarCarona(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	if resp, apto := exigeLogin(pedido, sessao); !apto {
		return resp
	}
	var req protocolo.CancelarCaronaReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil {
		return erro(pedido, "dados invalidos")
	}
	if err := s.CancelarCarona(sessao.Nome, req.CaronaID); err != nil {
		return erro(pedido, err.Error())
	}
	return ok(pedido, struct{}{})
}

// Trata o pedido de consultar passageiros de uma carona, verificando se o motorista é o dono da carona
func trataConsultarPassageiros(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	if resp, apto := exigeLogin(pedido, sessao); !apto {
		return resp
	}
	var req protocolo.ConsultarPassageirosReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil {
		return erro(pedido, "dados invalidos")
	}
	passageiros, err := s.estado.ConsultarPassageiros(sessao.Nome, req.CaronaID)
	if err != nil {
		return erro(pedido, err.Error())
	}
	lista := make([]protocolo.PassageiroTrecho, 0, len(passageiros))
	for _, p := range passageiros {
		lista = append(lista, protocolo.PassageiroTrecho{
			Passageiro:   p.Passageiro,
			TrechoInicio: p.TrechoInicio,
			TrechoFim:    p.TrechoFim,
		})
	}
	return ok(pedido, protocolo.ConsultarPassageirosResp{Passageiros: lista})
}

// Trata o pedido de buscar itinerários no consórcio, retornando uma lista de itinerários encontrados
func trataBuscarItinerarios(pedido protocolo.Pedido, s *Servidor) protocolo.Resposta {
	var req protocolo.BuscarItinerariosReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil {
		return erro(pedido, "dados invalidos")
	}
	if req.Origem == "" || req.Destino == "" || req.Data == "" {
		return erro(pedido, "origem, destino e data sao obrigatorios")
	}

	encontrados := BuscarItinerarios(s.caronasDoConsorcio(req.Data), req.Origem, req.Destino)
	itinerarios := make([]protocolo.ItinerarioEncontrado, 0, len(encontrados))
	for _, it := range encontrados {
		itinerarios = append(itinerarios, protocolo.ItinerarioEncontrado{Itens: paraItensPedido(it.itens), Preco: it.preco})
	}
	return ok(pedido, protocolo.BuscarItinerariosResp{Itinerarios: itinerarios})
}

// Trata o pedido de reservar um itinerário, coordenando a confirmação em todas as empresas envolvidas
func trataReservar(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	if resp, apto := exigeLogin(pedido, sessao); !apto {
		return resp
	}
	var req protocolo.ReservarReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil || len(req.Itens) == 0 {
		return erro(pedido, "itens invalidos")
	}

	itens := make([]dominio.ItemReserva, len(req.Itens))
	for i, it := range req.Itens {
		itens[i] = dominio.ItemReserva{
			Empresa:      it.Empresa,
			CaronaID:     it.CaronaID,
			TrechoInicio: it.TrechoInicio,
			TrechoFim:    it.TrechoFim,
		}
	}

	r, err := s.Reservar(sessao.Nome, itens)
	if err != nil {
		return erro(pedido, err.Error())
	}
	return ok(pedido, protocolo.ReservarResp{ReservaID: r.ID})
}

// Trata o pedido de cancelar uma reserva, cancelando-a em todas as empresas envolvidas
func trataCancelarReserva(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	if resp, apto := exigeLogin(pedido, sessao); !apto {
		return resp
	}
	var req protocolo.CancelarReservaReq
	if err := json.Unmarshal(pedido.Dados, &req); err != nil {
		return erro(pedido, "dados invalidos")
	}
	if err := s.CancelarReserva(sessao.Nome, req.ReservaID); err != nil {
		return erro(pedido, err.Error())
	}
	return ok(pedido, struct{}{})
}

// Trata o pedido de listar todas as reservas confirmadas do passageiro, retornando um resumo de cada uma.
func trataListarMinhasReservas(pedido protocolo.Pedido, s *Servidor, sessao *Sessao) protocolo.Resposta {
	if resp, apto := exigeLogin(pedido, sessao); !apto {
		return resp
	}
	reservas := s.estado.ListarReservas(sessao.Nome)
	resumos := make([]protocolo.ReservaResumo, 0, len(reservas))
	for _, r := range reservas {
		resumos = append(resumos, protocolo.ReservaResumo{ID: r.ID, Itens: paraItensPedido(r.Itens)})
	}
	return ok(pedido, protocolo.ListarMinhasReservasResp{Reservas: resumos})
}

// Converte os itens do domínio para o formato do protocolo
func paraItensPedido(itens []dominio.ItemReserva) []protocolo.ItemPedido {
	lista := make([]protocolo.ItemPedido, len(itens))
	for i, it := range itens {
		lista[i] = protocolo.ItemPedido{
			Empresa:      it.Empresa,
			CaronaID:     it.CaronaID,
			TrechoInicio: it.TrechoInicio,
			TrechoFim:    it.TrechoFim,
			Origem:       it.Origem,
			Destino:      it.Destino,
			Preco:        it.Preco,
		}
	}
	return lista
}
