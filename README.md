# Audit Sentinel Config [![Build Status](https://travis-ci.org/therealbill/audit-sentinel-config.svg?branch=master)](https://travis-ci.org/therealbill/audit-sentinel-config)

This tool is intended to run on a Sentinel node. It will troll through
the local sentinel config file looking for known errors, and issue a
report on what it finds.

Generally you want to run it with the '-report=all' flag.

# Important bits to know

This tool does a tad more than simply reading the config file and
checking it. It also connects to the Redis instances to do some
additional checking.  It also will connect to known sentinels to
validate the other sentinels are available. 

As such it needs to be run on the Sentinel and will expect to connect to
Redis masters, slaves, and other sentinels. Normally this shouldn't be a
problem as you should be running it on the Sentinel which also requires
this connectivity. 


# TODO List
 * add example "bad" sentinel config for testing
 * add example "good" sentinel config for testing
