package main

import (
	"fmt"
	"log"
	"manager/app"
	"manager/data"
	"os"
	"time"
)

const (
	DEBUG_FLAG      = false
	TIME_DEBUG_FLAG = true
)

func log_write(data string) {
	var file, err = os.OpenFile("manager.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err.Error())
	}
	logger := *log.New(file, "", 0)
	defer file.Close()

	logger.Output(1, data)
}

func main() {
	config := app.GetConfig("config.yml")
	kafkaconfig := app.SettingKafka{
		KafkaServerAddr: config.KafkaInfo.Host,
		KafkaServerPort: config.KafkaInfo.Port,
	}

	tcpRequestChan := make(chan *app.DataKey)
	tcpRequestKeys := app.DataKey{}
	tcpResponseData := make(chan []byte)

	current_time := app.ConsumerTime{}
	previous_time := app.ConsumerTime{}

	ch := app.ChannelStruct{}
	ch.ChannelInit()

	go app.TcpProcessing(tcpRequestChan, tcpResponseData, config.ApiServerInfo)

	app.ConsumerHostGroup(ch.ConsumerData.Host, kafkaconfig)
	app.ConsumerPerfGroup(ch.ConsumerData.Realtimeperf, kafkaconfig)
	app.ConsumerPIDGroup(ch.ConsumerData.Realtimepid, kafkaconfig)
	app.ConsumerDiskGroup(ch.ConsumerData.Realtimedisk, kafkaconfig)
	app.ConsumerNetGroup(ch.ConsumerData.Realtimenet, kafkaconfig)

	go app.GetDemoAgentinfo(ch.Agentinfo, config.Demo.HostCount)
	go app.GetDemoChangeStateAgent(ch.ChangeStateAgentStr, config.Demo)
	go app.GetDemoLastrealtimeperf(ch.Lastrealtimeperf, config.Demo)

	db_handler := make([]app.DBHandler, 0)
	agentinfo := <-ch.Agentinfo
	for _, dbinfo := range config.Database {
		db_handler = append(db_handler, *app.DBInit(dbinfo, config.Demo, agentinfo))
	}

	con_perf_arr := make([]data.AgentRealTimePerf, 0)
	con_pid_arr := make([]data.AgentRealTimePID, 0)
	con_disk_arr := make([]data.AgentRealTimeDisk, 0)
	con_net_arr := make([]data.AgentRealTimeNet, 0)

	dbdata := make([]app.DBDataStruct, len(db_handler))
	for i := 0; i < len(db_handler); i++ {
		dbdata[i] = app.DBDataStruct{
			Last: &data.LastrealtimeperfArray{},
			Perf: &data.RealtimeperfArray{},
			Cpu:  &data.RealtimecpuArray{},
			Pid:  &data.RealtimepidArray{},
			Proc: &data.RealtimeprocArray{},
			Disk: &data.RealtimediskArray{},
			Net:  &data.RealtimenetArray{},
		}
	}

	for {
		select {
		case state_agent_str := <-ch.ChangeStateAgentStr:
			// ??? ?????? TCP ???????????? ?????? ???????????? ??????, ????????? Host ????????? ????????? ????????? ??????
			// ??????????????? ????????? Agent ID??? ????????? ????????? ???
			for _, d := range db_handler {
				if tcpRequestKeys.IsDataMapping(app.HOST_CODE) {
					tcpResponseData <- app.ConvertJson(app.HOST_CODE, state_agent_str)
				}

				d.DemoHostStateChange(state_agent_str)
			}
		case lrtp := <-ch.Lastrealtimeperf:
			if tcpRequestKeys.IsDataMapping(app.LASTPERF_CODE) {
				for _, l := range lrtp.GetArrString() {
					tcpResponseData <- app.ConvertJson(app.LASTPERF_CODE, l)
				}
			}
			for _, d := range db_handler {
				d.DemoBptUpdate(lrtp)
			}
		case cshost := <-ch.ConsumerData.Host:
			if cshost.AgentID != "" {
				for idx, d := range db_handler {
					agentinfo_arr := data.AgentinfoArr{}
					d.SetHost(cshost, &agentinfo_arr)

					// TCP ???????????? 1?????? ???????????? ?????? ???
					if idx == 0 && tcpRequestKeys.IsDataMapping(app.HOST_CODE) {
						for _, a := range agentinfo_arr.GetArrString() {
							tcpResponseData <- app.ConvertJson(app.HOST_CODE, a)
						}
					}
				}
			}
		case csperf := <-ch.ConsumerData.Realtimeperf:
			if csperf.AgentID != "" {
				current_time.Perf = int64(time.Unix(time.Now().Unix(), 0).Unix() / 2)
				ltp_data := data.LastrealtimeperfArray{}
				perf_data := data.RealtimeperfArray{}
				cpu_data := data.RealtimecpuArray{}
				if DEBUG_FLAG {
					log_write(fmt.Sprintf("csperf %v\n", csperf))
				}

				for idx, d := range db_handler {
					dbtype := d.GetTabletype("realtimeperf")
					d.SetPerfArray(&con_perf_arr, dbtype, dbdata[idx].Last, dbdata[idx].Perf, dbdata[idx].Cpu)
				}

				db_handler[0].SetPerf(csperf, "pg", &ltp_data, &perf_data, &cpu_data)
				if tcpRequestKeys.IsDataMapping(app.LASTPERF_CODE) {
					go func() {
						tcpResponseData <- app.ConvertJson(app.LASTPERF_CODE, ltp_data.GetString())
					}()
				}
				if tcpRequestKeys.IsDataMapping(app.BASIC_CODE) {
					go func() {
						tcpResponseData <- app.ConvertJson(app.BASIC_CODE, perf_data.GetString())
					}()
				}
				if tcpRequestKeys.IsDataMapping(app.CPU_CODE) {
					go func() {
						tcpResponseData <- app.ConvertJson(app.CPU_CODE, cpu_data.GetString())
					}()
				}

				if len(con_perf_arr) > 0 && current_time.Perf > previous_time.Perf {
					con_perf_arr = app.RemoveDuplicate(con_perf_arr).([]data.AgentRealTimePerf)

					for idx, d := range db_handler {
						dbtype := d.GetTabletype("realtimeperf")

						// ?????????
						dbdata[idx].Last = &data.LastrealtimeperfArray{}
						dbdata[idx].Perf = &data.RealtimeperfArray{}
						dbdata[idx].Cpu = &data.RealtimecpuArray{}
						d.SetPerfArray(&con_perf_arr, dbtype, dbdata[idx].Last, dbdata[idx].Perf, dbdata[idx].Cpu)

						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimeperf before %v %d %d\n", idx, len(con_perf_arr), time.Now().UnixMicro()))
						}
						d.InsertTableArray(dbtype, dbdata[idx].Last, dbdata[idx].Perf, dbdata[idx].Cpu)
						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimeperf after %v %d %d\n", idx, len(con_perf_arr), time.Now().UnixMicro()))
						}
						// ?????????
						dbdata[idx].Last = &data.LastrealtimeperfArray{}
						dbdata[idx].Perf = &data.RealtimeperfArray{}
						dbdata[idx].Cpu = &data.RealtimecpuArray{}
					}

					con_perf_arr = nil
					previous_time.Perf = current_time.Perf
				} else {
					con_perf_arr = append(con_perf_arr, *csperf)
				}
			}
		case cspid := <-ch.ConsumerData.Realtimepid:
			if cspid.AgentID != "" {
				current_time.Pid = int64(time.Unix(time.Now().Unix(), 0).Unix() / 2)
				if DEBUG_FLAG {
					log_write(fmt.Sprintf("cspid %v\n", cspid))
				}
				for idx, d := range db_handler {
					dbtype := d.GetTabletype("realtimepid")
					d.SetPidArray(&con_pid_arr, dbtype, dbdata[idx].Pid, dbdata[idx].Proc)
				}

				if len(con_pid_arr) > 0 && current_time.Pid > previous_time.Pid {
					con_pid_arr = app.RemoveDuplicate(con_pid_arr).([]data.AgentRealTimePID)

					for idx, d := range db_handler {
						dbtype := d.GetTabletype("realtimepid")

						// ?????????
						dbdata[idx].Pid = &data.RealtimepidArray{}
						dbdata[idx].Proc = &data.RealtimeprocArray{}
						d.SetPidArray(&con_pid_arr, dbtype, dbdata[idx].Pid, dbdata[idx].Proc)

						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimepid before %v %d %d\n", idx, len(con_pid_arr), time.Now().UnixMicro()))
						}
						d.InsertTableArray(dbtype, dbdata[idx].Pid, dbdata[idx].Proc)
						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimepid after %v %d %d\n", idx, len(con_pid_arr), time.Now().UnixMicro()))
						}
						// ?????????
						dbdata[idx].Pid = &data.RealtimepidArray{}
						dbdata[idx].Proc = &data.RealtimeprocArray{}
					}

					con_pid_arr = nil
					previous_time.Pid = current_time.Pid
				} else {
					con_pid_arr = append(con_pid_arr, *cspid)
				}
			}
		case csdisk := <-ch.ConsumerData.Realtimedisk:
			if csdisk.AgentID != "" {
				current_time.Disk = int64(time.Unix(time.Now().Unix(), 0).Unix() / 2)
				tcp_data := data.RealtimediskArray{}
				if DEBUG_FLAG {
					log_write(fmt.Sprintf("csdisk %v\n", csdisk))
				}
				for idx, d := range db_handler {
					dbtype := d.GetTabletype("realtimedisk")
					d.SetDiskArray(&con_disk_arr, dbtype, dbdata[idx].Disk)
				}

				if tcpRequestKeys.IsDataMapping(app.DISK_CODE) {
					go func() {
						db_handler[0].SetDisk(csdisk, "pg", &tcp_data)
						tcpResponseData <- app.ConvertJson(app.DISK_CODE, tcp_data.GetString())
					}()
				}

				if len(con_disk_arr) > 0 && current_time.Disk > previous_time.Disk {
					con_disk_arr = app.RemoveDuplicate(con_disk_arr).([]data.AgentRealTimeDisk)

					for idx, d := range db_handler {
						dbtype := d.GetTabletype("realtimedisk")

						// ?????????
						dbdata[idx].Disk = &data.RealtimediskArray{}
						d.SetDiskArray(&con_disk_arr, dbtype, dbdata[idx].Disk)

						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimedisk before %v %d %d\n", idx, len(con_disk_arr), time.Now().UnixMicro()))
						}
						d.InsertTableArray(dbtype, dbdata[idx].Disk)
						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimedisk after %v %d %d\n", idx, len(con_disk_arr), time.Now().UnixMicro()))
						}
						// ?????????
						dbdata[idx].Disk = &data.RealtimediskArray{}
					}

					con_disk_arr = nil
					previous_time.Disk = current_time.Disk
				} else {
					con_disk_arr = append(con_disk_arr, *csdisk)
				}
			}
		case csnet := <-ch.ConsumerData.Realtimenet:
			if csnet.AgentID != "" {
				current_time.Net = int64(time.Unix(time.Now().Unix(), 0).Unix() / 2)
				tcp_data := data.RealtimenetArray{}
				if DEBUG_FLAG {
					log_write(fmt.Sprintf("csnet %v\n", csnet))
				}
				for idx, d := range db_handler {
					dbtype := d.GetTabletype("realtimenet")
					d.SetNetArray(&con_net_arr, dbtype, dbdata[idx].Net)
				}

				if tcpRequestKeys.IsDataMapping(app.NET_CODE) {
					go func() {
						db_handler[0].SetNet(csnet, "pg", &tcp_data)
						tcpResponseData <- app.ConvertJson(app.NET_CODE, tcp_data.GetString())
					}()
				}

				if len(con_net_arr) > 0 && current_time.Net > previous_time.Net {
					con_net_arr = app.RemoveDuplicate(con_net_arr).([]data.AgentRealTimeNet)

					for idx, d := range db_handler {
						dbtype := d.GetTabletype("realtimenet")
						// ?????????
						dbdata[idx].Net = &data.RealtimenetArray{}
						d.SetNetArray(&con_net_arr, dbtype, dbdata[idx].Net)

						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimenet before %v %d %d\n", idx, len(con_net_arr), time.Now().UnixMicro()))
						}
						d.InsertTableArray(dbtype, dbdata[idx].Net)
						if TIME_DEBUG_FLAG {
							log_write(fmt.Sprintf("realtimenet after %v %d %d\n", idx, len(con_net_arr), time.Now().UnixMicro()))
						}
						// ?????????
						dbdata[idx].Net = &data.RealtimenetArray{}
					}

					con_net_arr = nil
					previous_time.Net = current_time.Net
				} else {
					con_net_arr = append(con_net_arr, *csnet)
				}
			}
		case req_keys := <-tcpRequestChan:
			//fmt.Printf("main %v\n", req_keys)
			tcpRequestKeys = *req_keys
		}
	}
}
