.. Copyright (c) 2017 RackN Inc.
.. Licensed under the Apache License, Version 2.0 (the "License");
.. Digital Rebar Provision documentation under Digital Rebar master license
.. index::
  pair: Digital Rebar Provision; Install

.. _rs_install:

Install
~~~~~~~

The install script executes the following steps (in a slightly different order).

Get Code
--------

The code is delivered by zip file with a sha256sum to validate contents.  These are in github under the
`releases <https://github.com/digitalrebar/provision/releases>`_ tab for the Digital Rebar Provision project.

There are at least 3 releases to choose from:

  * **tip** - This is the most recent code.  This is the latest build of master.  It is bleeding edge and while the project attempts to be very stable with master, it can have issues.
  * **stable** - This is the most recent **stable** code.  This is a tag that tracks the version-based tag.
  * **v3.0.0** - There will be a set of Semantic Versioning named releases.

Previous releases will continue to be available in tag/release history.  For additional information, see
:ref:`rs_release_process`.

When using the **install.sh** script, the version can be specified by the **--drp-version** flag,
e.g. *--drp-version=v3.0.0*.

An example command sequence for Linux would be:

  ::

    mkdir dr-provision-install
    cd dr-provision-install
    curl -fsSL https://github.com/digitalrebar/provision/releases/download/tip/dr-provision.zip -o dr-provision.zip
    curl -fsSL https://github.com/digitalrebar/provision/releases/download/tip/dr-provision.sha256 -o dr-provision.sha256
    sha256sum -c dr-provision.sha256
    unzip dr-provision.zip

At this point, the **install.sh** script is available in the **tools** directory.  It can be used to continue the process or
continue following the steps in the next sections.  *tools/install.sh --help* will provide help and context information.

Prerequisites
-------------

**dr-provision** requires two applications to operate correctly, **bsdtar** and **7z**.  These are used to extract the contents
of iso and tar images to be served by the file server component of **dr-provision**.

For Linux, the **bsdtar** and **p7zip** packages are required.

.. admonition:: ubuntu

  sudo apt-get install -y bsdtar p7zip-full

.. admonition:: centos/redhat

  sudo yum install -y bsdtar p7zip

.. admonition:: Darwin

  The new package, **p7zip** is required, and **tar** must also be updated.  The **tar** program on Darwin is already **bsdtar**.

  * 7z - install from homebrew: brew install p7zip
  * libarchive - update from homebrew to get a functional tar: brew install libarchive

At this point, the server can be started.

Running The Server
------------------

Additional support materials in :ref:`rs_faq`.

The **install.sh** script provides two options for running **dr-provision**.  

The default values install the server and cli in /usr/local/bin.  It will also put a service control file in place.  Once that finishes,
the appropriate service start method will run the daemon.  The **install.sh** script prints out the command to run
and enable the service.  The method described in the :ref:`rs_quickstart` can be used to deploy this way if the
*--isolated* flag is removed from the command line.  Look at the internals of the **install.sh** script to see what
is going on.

Alternatively, the **install.sh** script can be provided the *--isolated* flag and it will setup the current directory
as an isolated "test drive" environment.  This will create a symbolic link from the bin directory to the local top-level
directory for the appropriate OS/platform, create a set of directories for data storage and file storage, and
display a command to run.  This is what the :ref:`rs_quickstart` method describes.

The default username & password is ``rocketskates:r0cketsk8ts``.

Please review `--help` for options like disabling services, logging or paths.

.. note:: sudo may be required to handle binding to the TFTP and DHCP ports.

Once running, the following endpoints are available:

* https://127.0.0.1:8092/swagger-ui - swagger-ui to explore the API
* https://127.0.0.1:8092/swagger.json - API Swagger JSON file
* https://127.0.0.1:8092/api/v3 - Raw api endpoint
* https://127.0.0.1:8092/ui - User Configuration Pages
* https://127.0.0.1:8091 - Static files served by http from the *test-data/tftpboot* directory
* udp 69 - Static files served from the test-data/tftpboot directory through the tftp protocol
* udp 67 - DHCP Server listening socket - will only serve addresses when once configured.  By default, silent.

The API, File Server, DHCP, and TFTP ports can be configured, but DHCP and TFTP may not function properly on non-standard ports.

If the SSL certificate is not valid, then follow the :ref:`rs_gen_cert` steps.

.. note:: On Darwin, it may be necessary to add a route for broadcast addresses to work.  This can be done with the below command.  The 192.168.100.1 is the IP address of the interface that the messages should be sent through.  The install script will provide suggestions.

  ::

    sudo route add 255.255.255.255 192.168.100.1
    # or < 10.9 OSX/Darwin
    sudo route -n add -net 255.255.255.255 192.168.100.1

