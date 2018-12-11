FROM ysangkok/i486-musl-cross
ENV CC /i486-musl-cross/bin/i486-linux-musl-gcc
ENV LD /i486-musl-cross/bin/i486-linux-musl-ld
ENV CXX /i486-musl-cross/bin/i486-linux-musl-g++
RUN ln -s /i486-musl-cross/bin/i486-linux-musl-gcc /i486-musl-cross/bin/gcc
RUN ln -s /i486-musl-cross/bin/i486-linux-musl-ld /i486-musl-cross/bin/ld
RUN ln -s /i486-musl-cross/bin/i486-linux-musl-g++ /i486-musl-cross/bin/g++
ENV CCBIN /i486-musl-cross/bin
ENV TOOLSBIN /i486-musl-cross/i486-linux-musl/bin
RUN ln -s $CCBIN/i486-linux-musl-objcopy $CCBIN/objcopy
ENV PATH $PATH:$CCBIN:/i486-musl-cross/bin:$TOOLSBIN


#make-4.1 uses OLDGNU tar format that busybox doesn't support, use our own tar
# alternative: https://sourceforge.net/projects/s-tar/files/
COPY sltar.c sltar.c
# alternative to make-4.1.tar.bz2: http://fossies.org/linux/privat/bmake-20141111.zip
RUN   $CC sltar.c -DVERSION="\"9000\"" -static -o sltar \
  &&  curl "http://ftp.gnu.org/gnu/make/make-4.2.1.tar.bz2" | bunzip2 | ./sltar x \
  &&  ( \
            cd make-4.2.1 \
        &&  ./configure PATH=$TOOLSBIN:$CCBIN:$PATH CC="$CC" LDFLAGS="-static" AR=$TOOLSBIN/ar RANLIB=$TOOLSBIN/ranlib \
        &&  ./build.sh \
      )
COPY ./patchelf-0.9 patchelf
RUN cd patchelf && ./configure LDFLAGS=-static
RUN cd patchelf/src; $TOOLSBIN/../lib/libc.so /make-4.2.1/make; cp patchelf /usr/bin
RUN patchelf --set-interpreter $TOOLSBIN/../lib/libc.so /make-4.2.1/make
RUN cd /make-4.2.1; cp ./make /usr/bin/make


RUN   curl http://ftp.gnu.org/gnu/m4/m4-1.4.17.tar.xz | unxz | tar x \
  &&  ( \
            cd m4-1.4.17 \
        &&  ./configure \
              LDFLAGS=-static \
              CC=$CC \
              AR=$TOOLSBIN/ar \
        RANLIB=$TOOLSBIN/ranlib \
        &&  make \
              PATH=$CCBIN:$PATH \
        &&  make install \
      ) \
  &&  rm -rf m4-1.4.17

RUN curl http://ftp.gnu.org/gnu/autoconf/autoconf-2.69.tar.xz | unxz | ./sltar x

RUN curl -L http://landley.net/toybox/bin/toybox-x86_64 > /usr/bin/comm && chmod +x /usr/bin/comm

RUN curl -L "https://downloads.sourceforge.net/project/bzip2/bzip2-1.0.6.tar.gz" | gunzip -cd | tar x
RUN cd bzip2-1.0.6 && sed -i.bak -re "s#AR=ar#AR=$TOOLSBIN/ar#g" Makefile
RUN cd bzip2-1.0.6 && sed -i.bak -re "s#LDFLAGS=#LDFLAGS=-static#g" Makefile
RUN cd bzip2-1.0.6 && make && make install

#RUN echo -e '#!/bin/sh\n$CC -static $@' > /usr/bin/cc && chmod +x /usr/bin/cc
RUN ln -s $TOOLSBIN/ranlib /usr/bin/ranlib
RUN curl -L http://search.cpan.org/CPAN/authors/id/A/AR/ARC/perl-5.27.2.tar.gz | gunzip -cd | ./sltar x
RUN cd perl-5.27.2 && sed -i.bak -re '1886s/exit 1/true/g' ./Configure && echo -en "y\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n-static" | ./Configure -Uusedl -Doptimize=-g -Accflags="-DNO_FANCY_MALLOC -DPLAIN_MALLOC" -Dcc=$CC && make || true
#RUN cd perl-5.27.2 && for i in ./perl $(find . -type f -name '*.so'); do patchelf --set-interpreter $TOOLSBIN/../lib/libc.so; done
RUN cd perl-5.27.2 && make && make install
RUN rm /usr/bin/ranlib
RUN /usr/local/bin/perl -e "exit 0"
ENV PATH /usr/local/bin:$PATH

RUN  curl http://ftp.gnu.org/gnu/bash/bash-4.4.tar.gz | gunzip | tar x \
  &&  ( \
            cd bash-4.4 \
         && ./configure \
              --prefix=/ \
              --without-bash-malloc \
              PATH=$CCBIN:$PATH \
              CC=$CC \
              LDFLAGS=-static \
              AR=$TOOLSBIN/ar \
              RANLIB=$TOOLSBIN/ranlib \
         && make -j2 \
              PATH=$CCBIN:$PATH \
         && make install\
      ) \
  &&  rm -rf bash-4.4

ENV CONFIG_SHELL /bin/bash
RUN cd autoconf-2.69 && bash ./configure && make -j2 && make install

RUN curl -L http://invisible-island.net/datafiles/release/byacc.tar.gz | gunzip -cd | ./sltar x
RUN cd byacc* && ./configure LDFLAGS=-static && make && make install

RUN curl http://ftp.gnu.org/gnu/patch/patch-2.7.5.tar.xz | unxz | ./sltar x
RUN cd patch-2.7.5 && perl -pi -e 's/^ *wait.*/true/g' configure
RUN cd patch-2.7.5 && touch configure $(find . -name Makefile.in) && sleep 1 && bash ./configure YACC=byacc LDFLAGS=-static \
              AR=$TOOLSBIN/ar \
              RANLIB=$TOOLSBIN/ranlib \
&& make -j2 && make install

RUN curl -L http://ftpmirror.gnu.org/libtool/libtool-2.4.6.tar.gz | gunzip -cd | tar x
RUN cd libtool-2.4.6 && perl -pi -e 's/^ *wait.*/true/g' configure
RUN cd libtool-2.4.6 && touch configure $(find . -name Makefile.in) && sleep 1 && ./configure LDFLAGS=-static AR=$TOOLSBIN/ar RANLIB=$TOOLSBIN/ranlib && make && make install

RUN curl -L https://pkg-config.freedesktop.org/releases/pkg-config-0.29.2.tar.gz | gunzip -cd | ./sltar x
RUN cd pkg-config* && perl -pi -e 's/^ *wait.*/true/g' configure glib/configure && touch $(find . -type f)
RUN cd pkg-config* && bash ./configure --with-internal-glib LDFLAGS=-static AR=$TOOLSBIN/ar RANLIB=$TOOLSBIN/ranlib && make -j2 && make install

RUN patchelf --set-interpreter $TOOLSBIN/../lib/libc.so /usr/local/bin/pkg-config

RUN curl -Lo boost.zip https://dl.bintray.com/boostorg/beta/1.65.0.beta.1/source/boost_1_65_0_beta1.zip
RUN unzip boost*.zip
RUN cd boost_* && perl -pi -e 's/BOOST_JAM_CC=gcc/BOOST_JAM_CC="gcc -static"/g' tools/build/src/engine/build.sh
RUN cd boost_* && bash ./bootstrap.sh
RUN cd boost_* && patchelf --set-interpreter $TOOLSBIN/../lib/libc.so ./b2
RUN cd boost_* && ./b2 install || true

RUN curl -Lo libressl.zip https://fossies.org/linux/misc/libressl-2.8.2.zip
RUN unzip libressl.zip
RUN cd libressl-* && perl -pi -e 's/^ *wait.*/true/g' configure
RUN cd libressl-* && ./configure --disable-asm LDFLAGS=-static AR=$TOOLSBIN/ar RANLIB=$TOOLSBIN/ranlib && make -j2 && make install

RUN curl -Lo automake.zip https://fossies.org/linux/misc/automake-1.16.1.zip
RUN unzip automake.zip
RUN cd automake-1.16.1 && perl -pi -e 's/^ *wait.*/true/g' configure
RUN cd automake-1.16.1 && touch $(find . -type f)
RUN echo -e "#!/bin/sh\nexit 0" > /usr/bin/makeinfo
RUN chmod +x /usr/bin/makeinfo
RUN cd automake-1.16.1 && bash configure LDFLAGS=-static && make
RUN cd automake-1.16.1 && make install

RUN curl -o bitcoin.zip -L https://github.com/bitcoin/bitcoin/archive/0.17.zip

RUN unzip bitcoin.zip

COPY patch patch
RUN cd bitcoin-* && patch -p1 < ../patch

RUN cd bitcoin-* && autoreconf -ivf

RUN curl -L https://github.com/libevent/libevent/releases/download/release-2.1.8-stable/libevent-2.1.8-stable.tar.gz | gunzip -cd | ./sltar x
RUN cd libevent* && perl -pi -e 's/^ *wait.*/true/g' configure
RUN cd libevent* && ./configure --disable-openssl LDFLAGS=-static AR=$TOOLSBIN/ar RANLIB=$TOOLSBIN/ranlib && make -j2 && make install

RUN cd bitcoin-* && perl -pi -e 's/^ *wait.*/true/g' $(find . -type f -name configure)
RUN cd bitcoin-* && perl -pi -e 's/\$\(CC_FOR_BUILD\) \$\^ -o \$@/\$(CC_FOR_BUILD) \$^ -static -o \$@/g' src/secp256k1/Makefile.am
RUN cd bitcoin-* && bash ./configure --disable-wallet --disable-shared --enable-debug CFLAGS=-static CXXFLAGS=-static LDFLAGS=-static AR=$TOOLSBIN/ar RANLIB=$TOOLSBIN/ranlib
RUN cd bitcoin-* && make -j2 V=1 || true
RUN cd bitcoin-*/src && /i486-musl-cross/bin/i486-linux-musl-g++ -std=c++11 -fstack-protector-all -o bitcoind bitcoind-bitcoind.o  libbitcoin_server.a libbitcoin_common.a univalue/.libs/libunivalue.a libbitcoin_util.a libbitcoin_consensus.a crypto/libbitcoin_crypto_base.a crypto/libbitcoin_crypto_sse41.a crypto/libbitcoin_crypto_avx2.a crypto/libbitcoin_crypto_shani.a leveldb/libleveldb.a leveldb/libleveldb_sse42.a leveldb/libmemenv.a secp256k1/.libs/libsecp256k1.a -L/usr/local/lib -lboost_system -lboost_filesystem -lboost_program_options -lboost_thread -lboost_chrono /usr/local/lib/libssl.a /usr/local/lib/libcrypto.a /usr/local/lib/libevent_pthreads.a /usr/local/lib/libevent.a /i486-musl-cross/i486-linux-musl/lib/libatomic.a -static
#RUN cd bitcoin-* && bash -c "patchelf --set-interpreter $TOOLSBIN/../lib/libc.so src/bitcoin{d,-tx,-cli}"
#RUN find / -iname '*libstdc++*'
RUN cd bitcoin-* && ls -lh ./src/bitcoind
RUN cd bitcoin-* && ./src/bitcoind --version
