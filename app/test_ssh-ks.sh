#!/usr/bin/env bash


. ./libtest.sh
DBG_RUN=1
# Debug-level for server
DBG_SRV=3
# Debug-level for client
DBG_CLIENT=3

main(){
    startTest
    build
    #test Build
    test Cothorityd
    #test ClientConfig
    #test ServerConfig
    #test ClientConfig
    #test ClientAdd
    #test ServerAdd
    #test ClientDel
    #test ServerDel
    stopTest
}

testCothorityd(){
    runCoCfg 1
    runCoCfg 2
    backg runCo 1
    backg runCo 2
    sleep 1
    cp co1/group.toml .
    tail -n 4 co2/group.toml >> group.toml
    testOK runCl 1 setup group.toml
}

testClientConfig(){
    runCl 1 list &
    runCl 2 list &
    sleep 1
    testFile cl1/config.bin
    testFile cl2/config.bin
    pkill -f ssh-ksc
}

testServerDel(){
    testServerAdd
    runCl 1 server del localhost:2001
    runCl 2 update
    runCl 2 confirm
    runCl 2 update
    testNGrep 2001 runCl 2 list

    testGrep 2001 runCl 1 list
    runCl 1 update
    testNGrep 2001 runCl 1 list
}
testClientDel(){
    testClientAdd
    runCl 1 clientRemove
    testGrep TestClient-cl1 runCl 2 list
    runCl 2 update
    runCl 2 confirm
    runCl 2 update
    testNGrep TestClient-cl1 runCl 2 list
}

testServerAdd(){
    runSrvCfg 1
    runSrvCfg 2
    runSrvCfg 3
    sleep .2
    runCl 1 server add localhost:2001
    runCl 1 server add localhost:2002
    testGrep 2001 runCl 1 list
    testGrep 2002 runCl 1 list

    runCl 2 server add localhost:2001
    testNGrep 2001 runCl 2 list
    testNGrep 2002 runCl 2 list
    runCl 2 server propose localhost:2001
    runCl 1 update
    runCl 1 confirm
    runCl 2 server add localhost:2001

    runCl 2 server add localhost:2003
    runCl 1 update
    runCl 1 confirm
    runCl 1 update
    runCl 1 listNew
    runCl 1 list
    testGrep 2003 runCl 1 list
}

testClientAdd(){
    runSrvCfg 1
    sleep .2
    runCl 1 server add localhost:2001
    sleep .2
    testGrep TestClient-cl1 runCl 1 list
    runCl 2 server add localhost:2001
    testGrep TestClient-cl2 runCl 2 list
    testNGrep TestClient-cl1 runCl 2 list

    runCl 2 server propose localhost:2001
    runCl 1 update
    testGrep TestClient-cl2 runCl 1 listNew
    #runCl 2 update
    #testNGrep TestClient-cl1 runCl 2 list
    runCl 1 confirm
    testGrep TestClient-cl2 runCl 1 list

    runCl 2 server add localhost:2001
    runCl 2 update
    testGrep TestClient-cl1 runCl 2 list
}

testServerConfig(){
    runSrvCfg 1
    runSrvCfg 2
    sleep 1
    testOK lsof -n -i:2001
    testOK lsof -n -i:2002
    pkill -f ssh-kss
    testFile srv1/config.bin
    testFile srv2/config.bin
}

testBuild(){
    testOK ./cothorityd help
    testOK ./ssh-kss help
    testOK ./ssh-ksc -c cl1 -cs cl1 help
}

runCl(){
    D=cl$1
    shift
    dbgRun ./ssh-ksc -d $DBG_CLIENT -c $D --cs $D $@
}

runSrvCfg(){
    echo -e "127.0.0.1:200$1\nsrv$1\nsrv$1\n" | runSrv $1 setup
}

runSrv(){
    nb=$1
    shift
    dbgRun ./ssh-kss -d $DBG_SRV -c srv$nb/config.bin $@
}

runCoCfg(){
    echo -e "127.0.0.1:200$1\nco$1\nco$1\n" | runCo $1 setup
}

runCo(){
    nb=$1
    shift
    dbgRun ./cothorityd -d $DBG_SRV -c co$nb/config.toml $@
}

build(){
    BUILDDIR=$(pwd)
    if [ "$STATICDIR" ]; then
        DIR=$STATICDIR
    else
        DIR=$(mktemp -d)
    fi
    mkdir -p $DIR
    cd $DIR
    echo "Building in $DIR"
    for app in cothorityd ssh-ksc ssh-kss; do
        if [ ! -e $app -o "$BUILD" ]; then
            if ! go build -o $app $BUILDDIR/$app/*.go; then
                fail "Couldn't build $app"
            fi
        fi
    done
    echo "Creating keys"
    for n in $(seq $NBR); do
        srv=srv$n
        if [ -d $srv ]; then
            rm -f $srv/*bin
        else
            mkdir $srv
            ssh-keygen -t rsa -b 4096 -N "" -f $srv/ssh_host_rsa_key > /dev/null
        fi

        cl=cl$n
        if [ -d $cl ]; then
            rm -f $cl/*bin
        else
            mkdir $cl
            ssh-keygen -t rsa -b 4096 -N "" -f $cl/id_rsa > /dev/null
        fi

        co=co$n
        rm -f $co/*
        mkdir -p $co
    done
}

if [ "$1" -a "$STATICDIR" ]; then
    rm -f $STATICDIR/*
fi

main