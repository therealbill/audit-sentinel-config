# Audit Sentinel Config [![Build Status](https://travis-ci.org/rackerlabs/audit-sentinel-config.svg?branch=master)](https://travis-ci.org/rackerlabs/audit-sentinel-config)

This tool is intended to run on a Sentinel node. It will troll through
the local sentinel config file looking for known errors, and issue a
report on what it finds.

Generally you want to run it with the '-report=all' flag.

# TODO List
 * add example "bad" sentinel config for testing
 * add example "good" sentinel config for testing
